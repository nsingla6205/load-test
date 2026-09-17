import re
import subprocess
import unittest
from pathlib import Path


class MultiTenancyChartRenderTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.chart = Path(__file__).resolve().parents[1]
        cls.repo = cls.chart.parents[1]
        command = [
            "helm",
            "template",
            "vsa-harvest-mt",
            str(cls.chart),
            "--set-string",
            "inventory.clusters[0].name=MixedCase-Cluster",
            "--set-string",
            "inventory.clusters[0].datacenter=us-test",
            "--set-string",
            "inventory.clusters[0].secret=test-secret",
        ]
        cls.rendered = subprocess.run(
            command,
            check=True,
            capture_output=True,
            text=True,
        ).stdout

    def test_enables_keyperf_after_existing_collectors(self):
        self.assertRegex(
            self.rendered,
            r"collectors:\s+(?:#.*\s+)+- Rest\s+- RestPerf\s+- KeyPerf",
        )
        self.assertIn("idle zeros", self.rendered)
        self.assertIn("RestPerf uses vol_* names", self.rendered)

    def test_uses_exact_cluster_name_only_for_tenancy_lookup(self):
        self.assertRegex(
            self.rendered,
            (
                r"name: TENANCY_CLUSTER_NAME\s+.*\s+.*\s+.*\s+"
                r'value: "MixedCase-Cluster"'
            ),
        )
        self.assertIn(
            """- --poller
            - "mixedcase-cluster" """.rstrip(),
            self.rendered,
        )
        self.assertIn("name: harvest-poller-mixedcase-cluster", self.rendered)

    def test_tenant_allowlist_adds_and_retains_metrics(self):
        match = re.search(r"regex: '([^']+)'", self.rendered)
        self.assertIsNotNone(
            match,
            "rendered OTel tenant metric regex not found",
        )
        metrics = set(match.group(1).split("|"))

        new_po_metrics = {"volume_snapshots_size_used"}
        service_control_only_metrics = {
            "volume_read_ops",
            "volume_write_ops",
            "volume_other_ops",
            "volume_inode_files_used",
            "volume_inode_files_total",
        }
        previous_metrics = {
            "volume_capacity",
            "volume_space_logical_used",
            "volume_read_latency",
            "volume_write_latency",
            "volume_other_latency",
            "volume_read_data",
            "volume_write_data",
            "volume_other_data",
            "snapmirror_total_transfer_bytes",
        }
        # MT does not own auto-tiering usage; these stay on the ST path only.
        auto_tiering_metrics = {
            "pool_capacity_tier_logical_footprint",
            "pool_client_protocol_reads",
            "pool_client_protocol_reads_raw",
            "pool_cloud_bin_operation_size",
            "pool_cloud_bin_operation_size_raw",
            "wafl_volume_client_protocol_reads",
            "wafl_volume_client_protocol_reads_raw",
            "wafl_volume_cloud_bin_operation_size",
            "wafl_volume_cloud_bin_operation_size_raw",
        }

        self.assertTrue(new_po_metrics.issubset(metrics))
        self.assertTrue(previous_metrics.issubset(metrics))
        self.assertTrue(service_control_only_metrics.isdisjoint(metrics))
        self.assertTrue(auto_tiering_metrics.isdisjoint(metrics))
        self.assertNotIn("pool_capacity", metrics)
        self.assertNotIn("pool_allocated", metrics)
        self.assertNotIn("volume_allocated_throughput", metrics)
        self.assertNotIn("volume_new_status", metrics)

    def test_consumer_mapping_matches_single_tenancy_ownership(self):
        # Harvest remains the consumer writer for these metrics.
        self.assertIn("netapp.googleapis.com/volume/snapshot_bytes", self.rendered)
        self.assertIn("netapp.googleapis.com/volume/operation_count", self.rendered)
        self.assertIn("netapp.googleapis.com/volume/inode_used", self.rendered)
        self.assertIn("netapp.googleapis.com/volume/inode_limit", self.rendered)

        # Telemetry remains the consumer writer for these metrics, as in ST.
        self.assertNotIn("netapp.googleapis.com/volume/throughput", self.rendered)
        self.assertNotIn("netapp.googleapis.com/volume/average_latency", self.rendered)
        self.assertNotIn("volume_allocated_throughput", self.rendered)
        self.assertNotIn("netapp.googleapis.com/volume/throughput_limit", self.rendered)
        self.assertNotIn("source: pool_capacity", self.rendered)
        self.assertNotIn("netapp.googleapis.com/storage_pool/capacity", self.rendered)
        self.assertNotIn("netapp.googleapis.com/storage_pool/allocated", self.rendered)
        self.assertNotRegex(self.rendered, r"mappings:\s+volume:[\s\S]*\n\s+pool:")

    def test_telemetry_does_not_republish_harvest_owned_metrics(self):
        metric_list = (
            self.repo / "telemetry" / "common" / "metricList.yaml"
        ).read_text()
        mt_metric_list = (
            self.repo / "telemetry" / "common" / "metricList_mt.yaml"
        ).read_text()

        for metric in (
            "volume_snapshots_size_used",
            "volume_read_ops",
            "volume_write_ops",
            "volume_other_ops",
            "volume_inode_files_used",
            "volume_inode_files_total",
        ):
            self.assertNotIn(f"- metric: {metric}", metric_list)

        self.assertIn("- metric: volume_snapshots_size_used", mt_metric_list)
        for metric in (
            "volume_read_ops",
            "volume_write_ops",
            "volume_other_ops",
            "volume_inode_files_used",
            "volume_inode_files_total",
        ):
            self.assertNotIn(f"- metric: {metric}", mt_metric_list)

        for metric in (
            "volume_read_data",
            "volume_write_data",
            "volume_other_data",
            "volume_read_latency",
            "volume_write_latency",
            "volume_other_latency",
        ):
            self.assertIn(f"- metric: {metric}", metric_list)

    def test_tenant_routing_still_uses_tenant_project(self):
        self.assertIn(
            'set(attributes["gcp.project.id"], attributes["tenant_project"])',
            self.rendered,
        )
        self.assertIn(
            'attributes["tenant_project"] == ""',
            self.rendered,
        )


if __name__ == "__main__":
    unittest.main()
