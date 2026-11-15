# Copyright 2026 Blink Labs Software
# SPDX-License-Identifier: Apache-2.0

"""Pure stdlib checks for smoke observations; no child Adder or network needed."""

import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location(
    "managed_mainnet_smoke", Path(__file__).with_name("test-managed-mainnet.py")
)
SMOKE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SMOKE)
HASH = "ab" * 32


def diagnostic(customer, rule, occurrence=None):
    record = {
        "component": "managed.router",
        "action": "filter_configured" if occurrence is None else "filter_matched",
        "customer_id": customer,
        "rule_id": rule,
        "event_type": SMOKE.RULES[rule],
        "revision": 1,
    }
    if occurrence is not None:
        record["occurrence_id"] = occurrence
    return record


def block_receipt(block_hash=HASH):
    return {
        "type": "input.block",
        "context": {"networkMagic": SMOKE.MAINNET_MAGIC},
        "payload": {"blockHash": block_hash},
    }


def non_block_receipt(rule="transactions", occurrence=2):
    if rule == "transactions":
        return {
            "type": "input.transaction",
            "context": {"transactionHash": f"{occurrence:064x}"},
            "payload": {"blockHash": HASH},
        }
    return {
        "type": "input.rollback",
        "payload": {"blockHash": HASH, "slotNumber": occurrence},
    }


class SmokeVerificationTests(unittest.TestCase):
    def setUp(self):
        self.configured = [
            diagnostic(customer, rule)
            for customer in SMOKE.CUSTOMERS
            for rule in SMOKE.RULES
        ]
        self.matched = {
            customer: [diagnostic(customer, "blocks", 1)]
            for customer in SMOKE.CUSTOMERS
        }
        self.events = {
            customer: [block_receipt()] for customer in SMOKE.CUSTOMERS
        }

    def verify(self, *, complete=False):
        return SMOKE.verify(
            self.configured, self.matched, self.events,
            network="mainnet", complete=complete,
        )

    def test_live_receipts_can_precede_all_match_diagnostics(self):
        self.matched = {customer: [] for customer in SMOKE.CUSTOMERS}
        summary = self.verify()
        self.assertFalse(summary["passed"])
        self.assertIsNone(summary["common_block"])
        self.assertEqual(15, summary["rules_loaded"])

    def test_live_file_poll_accepts_receipts_written_after_diagnostic_read(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "events").mkdir()
            records = self.configured + [
                record for matches in self.matched.values() for record in matches
            ]
            (root / "adder.log").write_text(
                "".join(json.dumps(record) + "\n" for record in records),
                encoding="utf-8",
            )
            for customer in SMOKE.CUSTOMERS:
                (root / "events" / (customer + ".jsonl")).write_text(
                    json.dumps(block_receipt()) + "\n", encoding="utf-8"
                )
            original_reader = SMOKE.json_lines

            def read_then_advance(path, **kwargs):
                snapshot = original_reader(path, **kwargs)
                if path.name == "adder.log":
                    for customer in SMOKE.CUSTOMERS:
                        with (root / "events" / (customer + ".jsonl")).open(
                            "a", encoding="utf-8"
                        ) as output:
                            output.write(json.dumps(non_block_receipt()) + "\n")
                return snapshot

            with mock.patch.object(SMOKE, "json_lines", side_effect=read_then_advance):
                observed = SMOKE.observations(root)
            summary = SMOKE.verify(*observed, network="mainnet")
            self.assertTrue(summary["passed"])
            self.assertEqual({"occurrence_id": 1, "block_hash": HASH}, summary["common_block"])
            for counts in summary["customers"].values():
                self.assertEqual(1, counts["matched_occurrences"])
                self.assertEqual(2, counts["local_log_receipts"])
            with self.assertRaisesRegex(ValueError, "receipts do not reconcile"):
                SMOKE.verify(*observed, network="mainnet", complete=True)

    def test_final_receipt_count_must_match_exactly(self):
        for receipts in ([], [block_receipt(), block_receipt()]):
            with self.subTest(receipts=len(receipts)):
                self.events["u1"] = receipts
                with self.assertRaisesRegex(ValueError, "receipts do not reconcile"):
                    self.verify(complete=True)

    def test_live_type_disagreement_defers_strict_order_check(self):
        self.events["u1"] = [non_block_receipt()]
        summary = self.verify()
        self.assertFalse(summary["passed"])
        with self.assertRaisesRegex(ValueError, "out of order or has an unexpected event type"):
            self.verify(complete=True)

    def test_final_reordered_receipts_fail_even_with_equal_counts(self):
        for customer in SMOKE.CUSTOMERS:
            self.matched[customer].append(diagnostic(customer, "transactions", 2))
            self.events[customer] = [non_block_receipt(), block_receipt()]
        self.assertFalse(self.verify()["passed"])
        with self.assertRaisesRegex(ValueError, "out of order or has an unexpected event type"):
            self.verify(complete=True)

    def test_live_hash_disagreement_defers_final_shared_occurrence_check(self):
        self.events["u1"] = [block_receipt("cd" * 32)]
        self.assertFalse(self.verify()["passed"])
        with self.assertRaisesRegex(ValueError, "different block hashes for the same occurrence"):
            self.verify(complete=True)

    def test_final_duplicate_and_out_of_order_matches_fail(self):
        for occurrences in ((1, 1), (2, 1)):
            with self.subTest(occurrences=occurrences):
                self.matched["u1"] = [
                    diagnostic("u1", "blocks", occurrence) for occurrence in occurrences
                ]
                self.events["u1"] = [block_receipt(), block_receipt()]
                with self.assertRaisesRegex(ValueError, "matched an occurrence twice or out of order"):
                    self.verify(complete=True)

    def test_final_rejects_a_customer_missing_a_matching_occurrence(self):
        for customer in SMOKE.CUSTOMERS:
            self.matched[customer].extend([
                diagnostic(customer, "transactions", 2),
                diagnostic(customer, "blocks", 3),
            ])
            self.events[customer].extend([
                non_block_receipt(), block_receipt("ef" * 32),
            ])
        original = copy.deepcopy((self.matched, self.events))
        for customer in SMOKE.CUSTOMERS:
            with self.subTest(customer=customer):
                self.matched, self.events = copy.deepcopy(original)
                self.matched[customer].pop(1)
                self.events[customer].pop(1)
                self.assertTrue(self.verify()["passed"])
                with self.assertRaisesRegex(ValueError, "ordered rule matches differ"):
                    self.verify(complete=True)

    def test_final_rejects_a_customer_with_an_extra_matched_occurrence(self):
        original = copy.deepcopy((self.matched, self.events))
        for customer in SMOKE.CUSTOMERS:
            with self.subTest(customer=customer):
                self.matched, self.events = copy.deepcopy(original)
                self.matched[customer].append(diagnostic(customer, "transactions", 2))
                self.events[customer].append(non_block_receipt())
                self.assertTrue(self.verify()["passed"])
                with self.assertRaisesRegex(ValueError, "ordered rule matches differ"):
                    self.verify(complete=True)

    def test_final_rejects_different_occurrence_ids_with_equal_customer_counts(self):
        for customer in SMOKE.CUSTOMERS:
            occurrence = 3 if customer == "u5" else 2
            self.matched[customer].append(diagnostic(customer, "transactions", occurrence))
            self.events[customer].append(non_block_receipt())
        self.assertTrue(self.verify()["passed"])
        with self.assertRaisesRegex(ValueError, "ordered rule matches differ"):
            self.verify(complete=True)

    def test_final_rejects_different_rule_order_with_equal_occurrence_ids(self):
        for customer in SMOKE.CUSTOMERS:
            rules = ("rollbacks", "transactions") if customer == "u5" else ("transactions", "rollbacks")
            for occurrence, rule in enumerate(rules, 2):
                self.matched[customer].append(diagnostic(customer, rule, occurrence))
                self.events[customer].append(non_block_receipt(rule, occurrence))
        self.assertTrue(self.verify()["passed"])
        with self.assertRaisesRegex(ValueError, "ordered rule matches differ"):
            self.verify(complete=True)

    def test_final_rejects_changed_non_block_receipt_identity(self):
        for rule in ("transactions", "rollbacks"):
            for changed in ("swapped", "duplicated"):
                with self.subTest(rule=rule, changed=changed):
                    self.setUp()
                    for customer in SMOKE.CUSTOMERS:
                        for occurrence in (2, 3):
                            self.matched[customer].append(diagnostic(customer, rule, occurrence))
                            self.events[customer].append(non_block_receipt(rule, occurrence))
                    self.assertTrue(self.verify(complete=True)["passed"])
                    if changed == "swapped":
                        self.events["u5"][1:] = reversed(self.events["u5"][1:])
                    else:
                        self.events["u5"][2] = copy.deepcopy(self.events["u5"][1])
                    with self.assertRaisesRegex(ValueError, "receipt identities differ"):
                        self.verify(complete=True)

    def test_transaction_identity_is_independent_of_asset_serialization_order(self):
        for customer in SMOKE.CUSTOMERS:
            self.matched[customer].append(diagnostic(customer, "transactions", 2))
            receipt = non_block_receipt()
            assets = [{"policyId": "a", "amount": "1"}, {"policyId": "b", "amount": "2"}]
            receipt["payload"]["outputs"] = [{"assets": assets if customer == "u1" else list(reversed(assets))}]
            self.events[customer].append(receipt)
        self.assertTrue(self.verify(complete=True)["passed"])

    def test_final_rejects_missing_receipt_identity(self):
        for rule, receipt in (
            ("transactions", {"type": "input.transaction", "payload": {}}),
            ("rollbacks", {"type": "input.rollback", "payload": {}}),
        ):
            with self.subTest(rule=rule):
                self.setUp()
                for customer in SMOKE.CUSTOMERS:
                    self.matched[customer].append(diagnostic(customer, rule, 2))
                    self.events[customer].append(receipt)
                with self.assertRaisesRegex(ValueError, "invalid identity"):
                    self.verify(complete=True)

    def test_final_transaction_identity_requires_its_chain_block(self):
        for block_hash in (None, "", "invalid", "F" * 64):
            with self.subTest(block_hash=block_hash):
                self.setUp()
                for customer in SMOKE.CUSTOMERS:
                    self.matched[customer].append(diagnostic(customer, "transactions", 2))
                    receipt = non_block_receipt()
                    receipt["payload"]["blockHash"] = block_hash
                    self.events[customer].append(receipt)
                with self.assertRaisesRegex(ValueError, "invalid identity"):
                    self.verify(complete=True)

    def test_caught_up_final_snapshot_reconciles_all_five_customers(self):
        for customer in SMOKE.CUSTOMERS:
            self.matched[customer].extend([
                diagnostic(customer, "transactions", 2),
                diagnostic(customer, "rollbacks", 3),
                diagnostic(customer, "blocks", 4),
            ])
            self.events[customer].extend([
                non_block_receipt(),
                non_block_receipt("rollbacks"),
                block_receipt("ef" * 32),
            ])
        original = copy.deepcopy((self.configured, self.matched, self.events))
        summary = self.verify(complete=True)
        self.assertTrue(summary["passed"])
        self.assertEqual(4, summary["observed_selected_occurrences"])
        for counts in summary["customers"].values():
            self.assertEqual(4, counts["matched_occurrences"])
            self.assertEqual(4, counts["local_log_receipts"])
            self.assertEqual({"blocks": 2, "transactions": 1, "rollbacks": 1}, counts["matches_per_rule"])
        self.assertEqual(original, (self.configured, self.matched, self.events))


class RouteReconciliationTests(unittest.TestCase):
    def setUp(self):
        self.matched = {
            customer: [diagnostic(customer, "blocks", 1)]
            for customer in SMOKE.CUSTOMERS
        }

    def reconcile(self, dispatches):
        records = [
            {"component": "managed.router", "action": "dispatched",
             "occurrence_id": occurrence, "event_type": event_type}
            for occurrence, event_type in dispatches
        ]
        for customer, matches in self.matched.items():
            records.extend(
                {"component": "managed.router", "action": "submitted_to_plugin",
                 "customer_id": customer, "destination_id": "local-log",
                 "occurrence_id": match["occurrence_id"], "revision": 1,
                 "rule_ids": [match["rule_id"]]}
                for match in matches
            )
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "adder.log").write_text(
                "".join(json.dumps(record) + "\n" for record in records),
                encoding="utf-8",
            )
            return SMOKE.reconcile_routes(root, self.matched)

    def test_rejects_selected_dispatch_omitted_by_every_customer(self):
        with self.assertRaisesRegex(ValueError, "matches do not reconcile with selected dispatches"):
            self.reconcile([(1, "input.block"), (2, "input.transaction")])

    def test_requires_event_type_to_select_expected_dispatches(self):
        for missing in (None, "", 3):
            with self.subTest(event_type=missing):
                with self.assertRaisesRegex(ValueError, "dispatch diagnostic has no event type"):
                    self.reconcile([(1, "input.block"), (2, missing)])

    def test_unselected_dispatch_does_not_require_customer_matches(self):
        result = self.reconcile([(1, "input.block"), (2, "input.governance")])
        self.assertEqual(2, result["dispatched_occurrences"])
        self.assertEqual({customer: 1 for customer in SMOKE.CUSTOMERS}, result["plugin_submissions"])

    def test_selected_dispatches_reconcile_for_every_customer(self):
        for customer in SMOKE.CUSTOMERS:
            self.matched[customer].extend([
                diagnostic(customer, "transactions", 2),
                diagnostic(customer, "rollbacks", 4),
            ])
        result = self.reconcile([
            (1, "input.block"), (2, "input.transaction"),
            (3, "input.governance"), (4, "input.rollback"),
        ])
        self.assertEqual({customer: 3 for customer in SMOKE.CUSTOMERS}, result["plugin_submissions"])


if __name__ == "__main__":
    unittest.main()
