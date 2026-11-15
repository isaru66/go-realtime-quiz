"""Render both Redis modes without modifying the environment overlays."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
KUBERNETES = ROOT / "deploy/kubernetes"


class KubernetesTopologyTests(unittest.TestCase):
    def test_environment_topologies(self):
        for environment in ("development", "production"):
            for replicas in (1, 6):
                with self.subTest(environment=environment, replicas=replicas):
                    with tempfile.TemporaryDirectory() as directory:
                        wrapper = Path(directory)
                        config = {
                            "apiVersion": "kustomize.config.k8s.io/v1beta1",
                            "kind": "Kustomization",
                            "resources": [
                                os.path.relpath(KUBERNETES / "overlays" / environment, wrapper)
                            ],
                        }
                        if replicas == 6:
                            config["components"] = [
                                os.path.relpath(KUBERNETES / "components/redis-cluster", wrapper)
                            ]
                        (wrapper / "kustomization.yaml").write_text(yaml.safe_dump(config))
                        rendered = subprocess.check_output(
                            ["kubectl", "kustomize", str(wrapper)], text=True
                        )
                    if output := os.environ.get("KUBERNETES_RENDER_DIR"):
                        Path(output, f"{environment}-{replicas}.yaml").write_text(rendered)
                    objects = {
                        (item["kind"], item["metadata"]["name"]): item
                        for item in yaml.safe_load_all(rendered)
                    }
                    redis = objects["StatefulSet", "redis"]["spec"]
                    self.assertEqual(redis["replicas"], replicas)
                    self.assertEqual(redis["updateStrategy"]["type"], "OnDelete")
                    hosts = [f"redis-{i}.redis" for i in range(replicas)]
                    consumers = [
                        (objects["Deployment", "backend"]["spec"], "REDIS_ADDRS",
                         ",".join(f"{host}:6379" for host in hosts)),
                        (objects["Job", "redis-init"]["spec"], "REDIS_NODES",
                         " ".join(hosts)),
                        (objects["CronJob", "redis-backup"]["spec"]["jobTemplate"]["spec"],
                         "REDIS_NODES", " ".join(hosts)),
                    ]
                    for spec, variable, expected in consumers:
                        env = spec["template"]["spec"]["containers"][0]["env"]
                        self.assertEqual(
                            next(entry["value"] for entry in env if entry["name"] == variable),
                            expected,
                        )
                    self.assertTrue(objects["CronJob", "redis-backup"]["spec"]["suspend"])
                    if environment == "production":
                        self.assertEqual(
                            redis["volumeClaimTemplates"][0]["spec"]["storageClassName"],
                            "managed-csi-premium",
                        )
                        self.assertIn(
                            "requiredDuringSchedulingIgnoredDuringExecution",
                            redis["template"]["spec"]["affinity"]["podAntiAffinity"],
                        )


if __name__ == "__main__":
    unittest.main()
