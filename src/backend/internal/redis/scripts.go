package redis

const snapshotScript = `
local raw=redis.call('HGET',KEYS[2],'data')
if not raw then return '' end
local m=cjson.decode(raw)
local t=redis.call('TIME'); local now=t[1]*1000+math.floor(t[2]/1000)
local players=redis.call('HGETALL',KEYS[4])
local answers={}
local quiz=redis.call('GET',KEYS[3])
local q=cjson.decode(quiz)
if m.index>=0 then
 local qid=q.questions[m.index+1].id
 for i=1,#players,2 do
  local a=redis.call('HGET',KEYS[6],'q:'..qid..':'..players[i])
  if a then table.insert(answers,players[i]);table.insert(answers,a) end
 end
end
local reference=redis.call('HGET',KEYS[6],'cmd:'..ARGV[1]..':'..ARGV[2]) or ''
return cjson.encode({meta=raw,quiz=quiz,players=players,answers=answers,
 scores=redis.call('ZRANGE',KEYS[7],0,-1,'WITHSCORES'),
 published=redis.call('ZRANGE',KEYS[8],0,-1,'WITHSCORES'),
 receipt=redis.call('HGET',KEYS[10],ARGV[1]..':'..ARGV[2]) or '',
 answer_receipt=reference,command_count=redis.call('HLEN',KEYS[10]),now=now})`

const createScript = `
local receipt=redis.call('GET',KEYS[13])
if receipt then return receipt end
local mapped=redis.call('GET',KEYS[1]) or ''
if mapped~=ARGV[4] then return 'retry' end
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
if mapped~='' then
 local old=redis.call('HGET',KEYS[14],'data')
 if old and cjson.decode(old).expires>now then return 'collision' end
end
local m=cjson.decode(ARGV[1])
m.created=now;m.activity=now;m.expires=now+math.min(m.idle_ms,m.lifetime_ms);m.purge=now+m.lifetime_ms+600000
local receipt=cjson.decode(ARGV[3]);receipt.expires=m.expires
redis.call('HSET',KEYS[2],'data',cjson.encode(m))
redis.call('SET',KEYS[3],ARGV[2])
redis.call('SET',KEYS[1],m.id)
redis.call('SET',KEYS[13],cjson.encode(receipt))
redis.call('ZADD',KEYS[11],m.expires,m.pin..':'..m.id)
redis.call('ZADD',KEYS[12],m.expires,m.pin..':'..m.id)
redis.call('XADD',KEYS[9],'MAXLEN',4096,'*','revision',1,'kind','game_created','timestamp',now)
for i=1,10 do redis.call('PEXPIREAT',KEYS[i],m.purge) end
redis.call('PEXPIREAT',KEYS[13],m.purge)
return cjson.encode(receipt)`

const mutateScript = `
local raw=redis.call('HGET',KEYS[2],'data')
if not raw then return 'session_unavailable' end
local old=cjson.decode(raw)
if old.revision~=tonumber(ARGV[1]) then return 'retry' end
local t=redis.call('TIME'); local now=t[1]*1000+math.floor(t[2]/1000)
local d=cjson.decode(ARGV[2]);local m=d.meta
if d.kind~='expire' and (old.state=='EXPIRED' or now>=old.expires) then return 'session_expired' end
if d.kind=='answer' and now>=old.deadline then return 'deadline_passed' end
if d.kind=='close' and now<old.deadline then return 'retry' end
if d.kind=='expire' and now<old.expires then return 'retry' end
if d.touch then m.activity=now end
if d.finished then m.finished_at=now end
m.expires=math.min(m.activity+m.idle_ms,m.created+m.lifetime_ms)
if m.finished_at>0 then m.expires=math.min(m.expires,m.finished_at+m.retention_ms) end
if d.duration>0 then m.started=now;m.deadline=math.min(now+d.duration,m.expires) end
if d.publish then m.closed_at=now end
m.revision=old.revision+1
if d.member then
 local p=d.member
 if d.kind=='join' or d.kind=='bind' then p.presence=now+45000 end
 redis.call('HSET',KEYS[4],p.id,cjson.encode(p))
 if p.left then redis.call('SREM',KEYS[5],p.id) else redis.call('SADD',KEYS[5],p.id) end
end
if d.answer then
 d.answer.accepted_at=now
 local encoded=cjson.encode(d.answer)
 redis.call('HSET',KEYS[6],d.answer_field,encoded,'cmd:'..d.player..':'..d.answer.command,encoded)
end
if d.score then redis.call('ZADD',KEYS[7],d.score,d.player) end
if d.kind=='join' then redis.call('ZADD',KEYS[7],0,d.player);redis.call('ZADD',KEYS[8],0,d.player) end
if d.publish then redis.call('ZUNIONSTORE',KEYS[8],1,KEYS[7]) end
if d.command_field~='' then redis.call('HSET',KEYS[10],d.command_field,d.command_value) end
redis.call('HSET',KEYS[2],'data',cjson.encode(m))
redis.call('XADD',KEYS[9],'MAXLEN',4096,'*','revision',m.revision,'kind',d.kind,'timestamp',now)
local member=m.pin..':'..m.id
if m.state=='EXPIRED' then
 redis.call('ZREM',KEYS[11],member);redis.call('ZREM',KEYS[12],member)
 if redis.call('GET',KEYS[1])==m.id then redis.call('DEL',KEYS[1]) end
else
 local due=m.expires
 if m.state=='QUESTION_OPEN' then due=math.min(due,m.deadline) end
 redis.call('ZADD',KEYS[11],due,member);redis.call('ZADD',KEYS[12],m.expires,member)
end
for i=2,10 do redis.call('PEXPIREAT',KEYS[i],m.purge) end
return 'ok'`

const heartbeatScript = `
local raw=redis.call('HGET',KEYS[1],ARGV[1])
if not raw then return 0 end
local p=cjson.decode(raw)
if p.connection~=ARGV[2] or p.left then return 0 end
local t=redis.call('TIME');p.presence=t[1]*1000+math.floor(t[2]/1000)+45000
redis.call('HSET',KEYS[1],ARGV[1],cjson.encode(p));return 1`

const dueScript = `
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
return redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',now,'LIMIT',0,100)`

const limitScript = `
local t=redis.call('TIME');local now=tonumber(t[1])
local window=math.floor(now/60)
local raw=redis.call('GET',KEYS[1])
local count=0
if raw then
 local v=cjson.decode(raw)
 if v.window==window then count=v.count end
end
count=count+1
redis.call('SET',KEYS[1],cjson.encode({window=window,count=count}),'EX',120)
if count>tonumber(ARGV[1]) then return 0 else return 1 end`

const answerScript = `
local raw=redis.call('HGET',KEYS[2],'data')
if not raw then return cjson.encode({error='session_unavailable'}) end
local m=cjson.decode(raw)
local t=redis.call('TIME');local now=t[1]*1000+math.floor(t[2]/1000)
local player=ARGV[1];local digest=ARGV[2];local connection=ARGV[3]
local command=ARGV[4];local question=ARGV[5];local option=ARGV[6]
local member=redis.call('HGET',KEYS[4],player)
if not member then return cjson.encode({error='invalid_credentials'}) end
local p=cjson.decode(member)
if p.digest~=digest then return cjson.encode({error='invalid_credentials'}) end
if now>=m.expires or m.state=='EXPIRED' then return cjson.encode({error='session_expired'}) end
if p.left then return cjson.encode({error='membership_left'}) end
if p.connection~=connection then return cjson.encode({error='connection_replaced'}) end
local receipt=redis.call('HGET',KEYS[6],'cmd:'..player..':'..command)
if receipt then
 local a=cjson.decode(receipt)
 if a.question_id~=question or a.option_id~=option then return cjson.encode({error='idempotency_conflict'}) end
 return cjson.encode({answer=a,revision=m.revision})
end
if redis.call('HEXISTS',KEYS[6],'q:'..question..':'..player)==1 then return cjson.encode({error='already_answered'}) end
if m.state~='QUESTION_OPEN' then return cjson.encode({error='invalid_state'}) end
local quiz=cjson.decode(redis.call('GET',KEYS[3]));local q=quiz.questions[m.index+1]
if q.id~=question then return cjson.encode({error='question_mismatch'}) end
if now>=m.deadline then return cjson.encode({error='deadline_passed'}) end
local valid=false
for _,o in ipairs(q.options) do if o.id==option then valid=true end end
if not valid then return cjson.encode({error='invalid_option'}) end
local points=0;local correct=option==q.correct_answer
if correct then points=q.points end
local a={question_id=question,option_id=option,command=command,accepted_at=now,correct=correct,points=points}
local encoded=cjson.encode(a)
redis.call('HSET',KEYS[6],'q:'..question..':'..player,encoded,'cmd:'..player..':'..command,encoded)
redis.call('ZINCRBY',KEYS[7],points,player)
m.revision=m.revision+1;m.activity=now;m.expires=math.min(now+m.idle_ms,m.created+m.lifetime_ms)
redis.call('HSET',KEYS[2],'data',cjson.encode(m))
redis.call('ZADD',KEYS[11],math.min(m.deadline,m.expires),m.pin..':'..m.id)
redis.call('ZADD',KEYS[12],m.expires,m.pin..':'..m.id)
redis.call('PEXPIREAT',KEYS[6],m.purge);redis.call('PEXPIREAT',KEYS[7],m.purge)
redis.call('XADD',KEYS[9],'MAXLEN',4096,'*','revision',m.revision,'kind','answer','timestamp',now)
return cjson.encode({answer=a,revision=m.revision})`
