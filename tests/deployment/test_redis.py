"""Real Redis deployment-script checks in uniquely scoped disposable containers."""

import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import unittest
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = ROOT / "deploy/kubernetes/base/scripts"
REDIS_IMAGE = "redis:7.4.7-alpine"


def docker(*args, check=True):
    return subprocess.run(
        ["docker", *args], text=True, capture_output=True, check=check, timeout=240
    )


class RedisTopologyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        docker("info")
        cls.backup_image = f"quiz-backup-test:{uuid.uuid4().hex}"
        docker("build", "-q", "-t", cls.backup_image, str(ROOT / "deploy/backup"))
        cls.addClassCleanup(docker, "image", "rm", cls.backup_image)

    def setUp(self):
        self.prefix = f"quiz-redis-test-{uuid.uuid4().hex[:12]}"
        self.password = secrets.token_hex(32)
        self.containers = []
        docker("network", "create", self.prefix)
        self.addCleanup(docker, "network", "rm", self.prefix)
        self.addCleanup(self.remove_containers)

    def remove_containers(self):
        if self.containers:
            docker("rm", "-f", "-v", *self.containers)

    def start_redis(self, count):
        hosts = []
        for index in range(count):
            host = f"redis-{index}"
            name = f"{self.prefix}-{host}"
            self.containers.append(name)
            docker(
                "run", "-d", "--name", name, "--network", self.prefix,
                "--network-alias", host, "--read-only", "--user", "redis",
                "-e", f"REDIS_PASSWORD={self.password}",
                "-e", f"REDIS_ANNOUNCE_HOSTNAME={host}",
                "-v", f"{SCRIPTS}:/opt/redis:ro",
                REDIS_IMAGE, "/bin/sh", "/opt/redis/start.sh",
            )
            hosts.append(host)
        return hosts

    def cli(self, host, *args):
        return docker(
            "exec", "-e", f"REDISCLI_AUTH={self.password}",
            f"{self.prefix}-{host}", "redis-cli", "-e", *args,
        ).stdout.strip()

    def initialize(self, hosts, fast=False):
        args = [
            "run", "--rm", "--network", self.prefix,
            "-e", f"REDIS_PASSWORD={self.password}",
            "-e", f"REDIS_NODES={' '.join(hosts)}",
            "-v", f"{SCRIPTS}:/opt/redis:ro",
        ]
        # Only refusal tests bypass retries; success tests wait for real convergence.
        command = "/bin/sh /opt/redis/initialize.sh"
        if fast:
            command = "sleep() { :; }; . /opt/redis/initialize.sh"
        return docker(*args, REDIS_IMAGE, "/bin/sh", "-c", command, check=False)

    def backup(self, hosts, *, changed=False, wrong_role=False):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            path.chmod(0o755)
            curl = path / "curl"
            curl.write_text("#!/bin/sh\ncat >> /work/uploads\nprintf '\\n' >> /work/uploads\n")
            curl.chmod(0o755)
            # Wrap real Redis responses to exercise the backup's refusal paths.
            redis_cli = path / "redis-cli"
            redis_cli.write_text(
                "#!/bin/sh\n"
                + ("case \"$*\" in *'info replication'*) echo role:slave; exit 0;; esac\n"
                   if wrong_role else "")
                + ("case \"$*\" in *'cluster nodes'*)\n"
                   "  n=0; [ ! -f /work/topology-count ] || n=$(cat /work/topology-count)\n"
                   "  n=$((n + 1)); echo \"$n\" > /work/topology-count\n"
                   "  /usr/local/bin/redis-cli \"$@\"\n"
                   "  [ \"$n\" -lt 3 ] || echo 'changed master - 0 0 1 connected 0-16383'\n"
                   "  exit 0;; esac\n" if changed else "")
                + "exec /usr/local/bin/redis-cli \"$@\"\n"
            )
            redis_cli.chmod(0o755)
            name = f"{self.prefix}-backup-{len(self.containers)}"
            self.containers.append(name)
            result = docker(
                "run", "--name", name, "--network", self.prefix,
                "-e", f"REDIS_PASSWORD={self.password}",
                "-e", f"REDIS_NODES={' '.join(hosts)}",
                "-e", "BLOB_CONTAINER_URL=https://example.blob.core.windows.net/test",
                "-e", "BLOB_SAS_TOKEN=local-test-only",
                "-e", "PATH=/test-bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                "-v", f"{path}:/test-bin:ro",
                "-v", f"{SCRIPTS}:/opt/backup:ro",
                self.backup_image, check=False,
            )
            docker("cp", f"{name}:/work", str(path / "output"))
            manifests = list((path / "output").glob("*/manifest.json"))
            if result.returncode:
                self.assertEqual(manifests, [])
                self.assertFalse((path / "output/uploads").exists())
                return result
            self.assertEqual(len(manifests), 1)
            manifest = json.loads(manifests[0].read_text())
            self.assertEqual(len(manifest["files"]), 1 if len(hosts) == 1 else 3)
            for entry in manifest["files"]:
                data = (manifests[0].parent / entry["rdb"]).read_bytes()
                self.assertEqual(hashlib.sha256(data).hexdigest(), entry["sha256"])
            uploads = (path / "output/uploads").read_text().strip().splitlines()
            self.assertIn("/manifest.json?", uploads[-1])
            return result

    def test_single_node_bootstrap_restart_and_backup(self):
        hosts = self.start_redis(1)
        result = self.initialize(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        node_id = self.cli(hosts[0], "cluster", "myid")
        self.cli(hosts[0], "set", "retained", "yes")
        result = self.initialize(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(self.cli(hosts[0], "cluster", "myid"), node_id)
        docker("restart", f"{self.prefix}-{hosts[0]}")
        result = self.initialize(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual(self.cli(hosts[0], "get", "retained"), "yes")
        result = self.backup(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        for options, message in [
            ({"changed": True}, "Topology changed"),
            ({"wrong_role": True}, "Expected exactly 1 primaries"),
        ]:
            result = self.backup(hosts, **options)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)

    def test_six_node_bootstrap_repeat_and_backup(self):
        hosts = self.start_redis(6)
        for _ in range(2):
            result = self.initialize(hosts)
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        result = self.backup(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        result = self.initialize(hosts[:1])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("topology mismatch", result.stderr)
        result = self.backup(hosts[:1])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("health/topology mismatch", result.stderr)

    def test_invalid_endpoints(self):
        for hosts, message in [
            (["a", "b"], "Expected one or six"),
            (["a"] * 6, "Duplicate Redis endpoints"),
        ]:
            result = self.initialize(hosts)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)
            result = self.backup(hosts)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)

    def test_partial_and_mixed_state_is_not_rewritten(self):
        hosts = self.start_redis(6)
        # Wait for Redis processes, not bootstrap, before constructing partial state.
        for host in hosts:
            for _ in range(60):
                try:
                    self.cli(host, "ping")
                    break
                except subprocess.CalledProcessError:
                    time.sleep(0.1)
            else:
                self.fail(f"{host} did not start")
        self.cli(hosts[0], "cluster", "addslotsrange", "0", "100")
        node_id = self.cli(hosts[0], "cluster", "myid")
        for endpoints in (hosts[:1], hosts):
            result = self.initialize(endpoints, fast=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("No state was reset" if len(endpoints) == 1 else "topology mismatch",
                          result.stderr)
            self.assertIn("cluster_slots_assigned:101", self.cli(hosts[0], "cluster", "info"))
            self.assertEqual(self.cli(hosts[0], "cluster", "myid"), node_id)

    @unittest.skipUnless(os.environ.get("QUIZ_BACKEND_IMAGE"), "set QUIZ_BACKEND_IMAGE for API smoke")
    def test_single_node_cross_backend_lifecycle(self):
        hosts = self.start_redis(1)
        result = self.initialize(hosts)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        urls = []
        for index in range(2):
            name = f"{self.prefix}-backend-{index}"
            self.containers.append(name)
            docker(
                "run", "-d", "--name", name, "--network", self.prefix,
                "-p", "127.0.0.1::8080",
                "-e", f"REDIS_PASSWORD={self.password}", "-e", "REDIS_ADDRS=redis-0:6379",
                "-e", f"APP_SECRET={self.password}", "-e", f"ADMIN_CREDENTIAL={self.password}",
                "-e", "COOKIE_SECURE=false", "-e", "ALLOWED_ORIGINS=http://localhost:8088",
                "-e", "QUIZ_PATH=/config/quizzes.yaml",
                "-e", "PROTOCOL_SCHEMA_PATH=/app/protocol-v1.schema.json",
                "-v", f"{ROOT / 'config'}:/config:ro", os.environ["QUIZ_BACKEND_IMAGE"],
            )
            address = docker("port", name, "8080/tcp").stdout.strip()
            url = f"http://{address}"
            for _ in range(60):
                try:
                    with urllib.request.urlopen(url + "/readyz", timeout=2) as response:
                        self.assertEqual(response.status, 200)
                    break
                except OSError:
                    time.sleep(0.5)
            else:
                self.fail(docker("logs", name).stdout)
            urls.append(url)
        env = {
            **os.environ, "BACKEND_A_URL": urls[0], "BACKEND_B_URL": urls[1],
            "ADMIN_CREDENTIAL": self.password, "QUIZ_ALLOW_FAILURE_TESTS": "false",
        }
        subprocess.run(
            ["go", "test", "-tags=integration", "./internal/transport",
             "-run", "TestDistributed", "-count=1", "-timeout=4m"],
            cwd=ROOT / "src/backend", env=env, check=True, timeout=260,
        )


if __name__ == "__main__":
    unittest.main()
