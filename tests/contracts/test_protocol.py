"""Executable Phase 2 contract checks; no backend or Redis server required."""

import binascii
from copy import deepcopy
import json
from pathlib import Path
import re
import unittest

from jsonschema import Draft202012Validator, FormatChecker


ROOT = Path(__file__).resolve().parents[2]
SCHEMA = json.loads((ROOT / "docs/protocol-v1.schema.json").read_text())
EXAMPLES = json.loads((ROOT / "docs/protocol-v1.examples.json").read_text())


def validator(direction):
    schema = deepcopy(SCHEMA)
    schema.pop("oneOf")
    schema["$ref"] = f"#/$defs/{direction}"
    return Draft202012Validator(schema, format_checker=FormatChecker())


class ProtocolContractTests(unittest.TestCase):
    def setUp(self):
        self.client = validator("client")
        self.server = validator("server")
        self.join = deepcopy(EXAMPLES[0]["event"])
        self.joined = deepcopy(EXAMPLES[1]["event"])
        self.answer = deepcopy(EXAMPLES[2]["event"])
        self.received = deepcopy(EXAMPLES[3]["event"])
        self.reconnect = deepcopy(EXAMPLES[4]["event"])
        self.recovered = deepcopy(EXAMPLES[5]["event"])
        self.ended = deepcopy(EXAMPLES[6]["event"])

    def assertInvalid(self, direction, event):
        self.assertTrue(list(validator(direction).iter_errors(event)), event)

    def test_schema_and_documented_examples(self):
        Draft202012Validator.check_schema(SCHEMA)
        for example in EXAMPLES:
            with self.subTest(event=example["event"]["event_type"]):
                validator(example["direction"]).validate(example["event"])
                opposite = "server" if example["direction"] == "client" else "client"
                self.assertInvalid(opposite, example["event"])

    def test_all_client_commands(self):
        payloads = {
            "join_game": self.join["payload"],
            "reconnect": self.reconnect["payload"],
            "host_connect": {
                "host_capability": "A" * 43,
                "csrf_token": "A" * 43,
            },
            "submit_answer": self.answer["payload"],
            "leave_game": {},
            "start_game": {"expected_control_revision": 0},
            "next_question": {"expected_control_revision": 2},
            "end_game": {"expected_control_revision": 1},
            "request_snapshot": {},
            "ping": {},
        }
        for event_type, payload in payloads.items():
            with self.subTest(event=event_type):
                event = deepcopy(self.answer)
                event.update(event_type=event_type, payload=payload)
                if event_type in ("host_connect", "start_game", "next_question", "end_game"):
                    event.pop("player_id")
                self.client.validate(event)

    def test_all_server_events(self):
        snapshot = self.recovered["payload"]
        host_snapshot = deepcopy(snapshot)
        host_snapshot["role"] = "host"
        host_snapshot.pop("own_score")
        host_snapshot.pop("own_answer")
        error = {
            "code": "temporarily_unavailable",
            "message": "Please retry.",
            "retryable": True,
            "retry_after_ms": 1000,
        }
        member = snapshot["roster"][0]
        payloads = {
            "game_joined": snapshot,
            "reconnect_success": snapshot,
            "host_connected": host_snapshot,
            "state_snapshot": snapshot,
            "player_joined": member,
            "player_left": {**member, "left": True, "connected": False},
            "game_started": {},
            "question_started": snapshot["question"],
            "question_timer": {
                "question_id": "q1",
                "server_time": snapshot["server_time"],
                "deadline": snapshot["question"]["deadline"],
            },
            "answer_received": self.received["payload"],
            "question_ended": self.ended["payload"],
            "answer_result": {
                "question_id": "q1",
                "correct_option_id": "A",
                "answered": True,
                "correct": True,
                "awarded_points": 1000,
            },
            "answer_statistics": {
                "question_id": "q1", "counts": {"A": 1, "B": 0}, "answer_count": 1,
            },
            "leaderboard_updated": {"leaderboard": snapshot["leaderboard"]},
            "game_finished": {},
            "game_left": {},
            "command_accepted": {},
            "pong": {"server_time": snapshot["server_time"]},
            "server_draining": {},
            "error": error,
            "reconnect_failed": error,
        }
        for event_type, payload in payloads.items():
            with self.subTest(event=event_type):
                event = deepcopy(self.received)
                event.update(event_type=event_type, payload=payload)
                if event_type == "host_connected":
                    event.pop("player_id")
                self.server.validate(event)

    def test_required_identity_and_closed_command_shapes(self):
        for field in ("player_id", "session_id", "event_id"):
            event = deepcopy(self.answer)
            event.pop(field)
            with self.subTest(missing=field):
                self.assertInvalid("client", event)
        for field, value in (
            ("protocol_version", 2),
            ("game_pin", 123456),
            ("game_pin", "012345"),
            ("event_id", "not-a-uuid"),
            ("event_id", "406bb60d-0e91-1bc3-9747-f67354d662aa"),
            ("timestamp", "yesterday"),
            ("timestamp", "2026-02-30T08:30:00.000Z"),
            ("timestamp", "2026-11-15T08:30:00Z"),
            ("timestamp", "2026-11-15T08:30:00+00:00"),
            ("event_type", "unknown"),
            ("revision", 4),
            ("reply_to", self.answer["event_id"]),
        ):
            event = deepcopy(self.answer)
            event[field] = value
            with self.subTest(field=field, value=value):
                self.assertInvalid("client", event)
        for field, value in (("points", 1000), ("correct", True), ("option_id", "A:other")):
            event = deepcopy(self.answer)
            event["payload"][field] = value
            self.assertInvalid("client", event)
        for token in ("short", "A" * 44, "+" * 43):
            event = deepcopy(self.join)
            event["payload"]["reconnect_token"] = token
            self.assertInvalid("client", event)

    def test_open_question_and_acknowledgment_redaction(self):
        leaks = {
            "correct_option_id": "A",
            "correct_answer": "A",
            "awarded_points": 1000,
            "correct": True,
            "reconnect_token": "A" * 43,
        }
        for field, value in leaks.items():
            event = deepcopy(self.received)
            event["payload"][field] = value
            with self.subTest(ack_leak=field):
                self.assertInvalid("server", event)
            event = deepcopy(self.recovered)
            event["payload"]["question"][field] = value
            with self.subTest(question_leak=field):
                self.assertInvalid("server", event)
        for field, value in (
            ("closed_result", self.ended["payload"]),
            ("own_result", {
                "question_id": "q1", "correct_option_id": "A",
                "answered": True, "correct": True, "awarded_points": 1000,
            }),
            ("statistics", {"question_id": "q1", "counts": {"A": 1, "B": 0}, "answer_count": 1}),
        ):
            event = deepcopy(self.recovered)
            event["payload"][field] = value
            self.assertInvalid("server", event)

    def test_closed_results_and_role_boundaries(self):
        event = deepcopy(self.recovered)
        event["payload"].update(
            state="QUESTION_CLOSED",
            closed_result=self.ended["payload"],
            own_result={
                "question_id": "q1", "correct_option_id": "A",
                "answered": True, "correct": True, "awarded_points": 1000,
            },
            own_score=1000,
        )
        self.server.validate(event)
        event["payload"].pop("closed_result")
        self.assertInvalid("server", event)
        event = deepcopy(self.recovered)
        event["payload"]["role"] = "host"
        self.assertInvalid("server", event)
        event["payload"].pop("own_score")
        event["payload"].pop("own_answer")
        # A player recovery reply must not acquire a host role.
        self.assertInvalid("server", event)
        event["event_type"] = "host_connected"
        event.pop("player_id")
        self.server.validate(event)
        event["payload"]["own_score"] = 1000
        self.assertInvalid("server", event)

    def test_expired_snapshot_has_no_game_details(self):
        event = deepcopy(self.joined)
        event["event_type"] = "state_snapshot"
        event["payload"].update(state="EXPIRED", roster=[], leaderboard=[])
        event["payload"].pop("own_score")
        self.server.validate(event)
        for field, value in (
            ("question", self.recovered["payload"]["question"]),
            ("roster", self.joined["payload"]["roster"]),
            ("leaderboard", self.joined["payload"]["leaderboard"]),
            ("own_score", 1000),
        ):
            leaked = deepcopy(event)
            leaked["payload"][field] = value
            self.assertInvalid("server", leaked)

    def test_unauthenticated_error_without_fabricated_session(self):
        event = deepcopy(self.join)
        event.update(event_type="error", reply_to=event["event_id"])
        event["payload"] = {
            "code": "wrong_pin", "message": "Game not found.", "retryable": False,
        }
        self.server.validate(event)
        event["payload"]["reconnect_token"] = "A" * 43
        self.assertInvalid("server", event)


class DesignIntegrityTests(unittest.TestCase):
    def test_partition_key_slots_and_pin_routing(self):
        slots = set()
        for partition in range(64):
            tag = f"quiz:p{partition:02d}"
            slots.add(binascii.crc_hqx(tag.encode(), 0) % 16384)
        self.assertEqual(len(slots), 64, "coordination tags must not collide")
        self.assertEqual(123456 % 64, 0)
        for pin in (100000, 123456, 999999):
            prefix = f"quiz:v1:{{quiz:p{pin % 64:02d}}}:"
            keys = [prefix + suffix for suffix in (
                f"pin:{pin}", "game:session", "quiz:session", "players:session",
                "participants:session", "answers:session", "scores:session",
                "published:session", "events:session", "commands:session",
                "due", "active", "create:owner:request",
            )]
            key_slots = {
                binascii.crc_hqx(re.search(r"\{([^}]+)\}", key)[1].encode(), 0) % 16384
                for key in keys
            }
            self.assertEqual(len(key_slots), 1)

    def test_relative_document_links_and_code_fences(self):
        documents = [ROOT / "README.md", *sorted((ROOT / "docs").glob("*.md"))]
        for document in documents:
            text = document.read_text()
            with self.subTest(document=document.name):
                self.assertEqual(len(re.findall(r"^```", text, re.MULTILINE)) % 2, 0)
                for target in re.findall(r"(?<!!)\[[^\]]+\]\(([^)]+)\)", text):
                    if "://" in target or target.startswith("#"):
                        continue
                    self.assertTrue((document.parent / target.split("#")[0]).exists(), target)


if __name__ == "__main__":
    unittest.main()
