#!/usr/bin/env python3
# Copyright 2026 Blink Labs Software
# SPDX-License-Identifier: Apache-2.0

"""Run five managed customer lanes against a live ChainSync source."""

import argparse
import json
import os
from pathlib import Path
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request


CUSTOMERS = tuple(f"u{i}" for i in range(1, 6))
RULES = {"blocks": "input.block", "transactions": "input.transaction", "rollbacks": "input.rollback"}
MAINNET_MAGIC = 764824073
METADATA_FIELDS = {"time", "level", "component", "action", "customer_id", "rule_id", "event_type", "revision", "occurrence_id"}


def write_json(path, value):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")
    temporary.replace(path)


def prepare(root):
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    if any(root.iterdir()):
        raise ValueError("output directory must be new or empty")
    root.chmod(0o700)
    for name in ("config", "credentials", "events", "filter-logs"):
        (root / name).mkdir(mode=0o700)
    refs = []
    for customer in CUSTOMERS:
        write_json(root / "config" / f"{customer}.json", {
            "id": customer,
            "revision": 1,
            "destinations": [{"id": "local-log", "plugin": "log", "options": {"path": f"{customer}.jsonl", "format": "json"}}],
            "filters": [{"id": name, "event_type": event_type, "destination": "all"} for name, event_type in RULES.items()],
        })
        refs.append({"id": customer, "revision": 1, "file": f"{customer}.json"})
    write_json(root / "config" / "manifest.json", {"generation": 1, "customers": refs})
    token = secrets.token_urlsafe(48)
    fd = os.open(root / "operator-token", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as output:
        output.write(token + "\n")
    return token


def json_lines(path, *, diagnostic=False, complete=False):
    if not path.exists():
        return []
    records = []
    with path.open(encoding="utf-8") as stream:
        for number, line in enumerate(stream, 1):
            if not line.endswith("\n") and not complete:
                break
            try:
                value = json.loads(line)
            except json.JSONDecodeError:
                if diagnostic:
                    continue
                raise ValueError(f"invalid JSON in {path.name} at line {number}") from None
            if not isinstance(value, dict):
                if diagnostic:
                    continue
                raise ValueError(f"non-object event in {path.name} at line {number}")
            records.append(value)
    return records


def observations(root, *, complete=False):
    diagnostics = json_lines(root / "adder.log", diagnostic=True, complete=complete)
    configured = []
    matched = {customer: [] for customer in CUSTOMERS}
    for record in diagnostics:
        if record.get("component") != "managed.router":
            continue
        action = record.get("action")
        if action not in ("filter_configured", "filter_matched"):
            continue
        customer, rule = record.get("customer_id"), record.get("rule_id")
        if customer not in matched or rule not in RULES:
            raise ValueError("unexpected customer or rule in filter diagnostics")
        if record.get("event_type") != RULES[rule] or record.get("revision") != 1:
            raise ValueError("filter diagnostic has an unexpected type or revision")
        if any(key in record for key in ("payload", "credentials", "token", "url")):
            raise ValueError("filter diagnostic contains non-metadata fields")
        metadata = {key: value for key, value in record.items() if key in METADATA_FIELDS}
        if action == "filter_configured":
            configured.append(metadata)
        else:
            occurrence = record.get("occurrence_id")
            if not isinstance(occurrence, int) or isinstance(occurrence, bool) or occurrence < 1:
                raise ValueError("filter match has no positive occurrence ID")
            matched[customer].append(metadata)
    events = {customer: json_lines(root / "events" / f"{customer}.jsonl", complete=complete) for customer in CUSTOMERS}
    return configured, matched, events


def verify(configured, matched, events, *, network, complete=False):
    expected = {(customer, rule) for customer in CUSTOMERS for rule in RULES}
    loaded = [(record["customer_id"], record["rule_id"]) for record in configured]
    if len(loaded) != len(set(loaded)):
        raise ValueError("a rule was configured more than once in the initial revision")
    ready = set(loaded) == expected
    blocks = {}
    counts = {}
    for customer in CUSTOMERS:
        matches = matched[customer]
        receipts = events[customer]
        occurrences = [record["occurrence_id"] for record in matches]
        if occurrences != sorted(set(occurrences)):
            raise ValueError(f"{customer} matched an occurrence twice or out of order")
        if len(receipts) > len(matches) or (complete and len(receipts) != len(matches)):
            raise ValueError(f"{customer} plugin receipts do not reconcile with rule matches")
        by_occurrence = {}
        for match, receipt in zip(matches, receipts):
            if receipt.get("type") != match["event_type"]:
                raise ValueError(f"{customer} plugin receipt is out of order or has an unexpected event type")
            if receipt["type"] == "input.block":
                payload = receipt.get("payload", {})
                if not isinstance(payload, dict):
                    raise ValueError(f"{customer} block receipt has no object payload")
                block_hash = payload.get("blockHash")
                if not isinstance(block_hash, str) or len(block_hash) != 64 or any(c not in "0123456789abcdef" for c in block_hash):
                    raise ValueError(f"{customer} block receipt has an invalid hash")
                context = receipt.get("context")
                if network == "mainnet" and (not isinstance(context, dict) or context.get("networkMagic") != MAINNET_MAGIC):
                    raise ValueError(f"{customer} block receipt is not from mainnet")
                by_occurrence[match["occurrence_id"]] = block_hash
        blocks[customer] = by_occurrence
        counts[customer] = {
            "rules_loaded": sum(record["customer_id"] == customer for record in configured),
            "matches_per_rule": {rule: sum(record["rule_id"] == rule for record in matches) for rule in RULES},
            "matched_occurrences": len(matches),
            "local_log_receipts": len(receipts),
        }
    common = set.intersection(*(set(blocks[customer]) for customer in CUSTOMERS))
    witnesses = []
    for occurrence in sorted(common):
        hashes = {blocks[customer][occurrence] for customer in CUSTOMERS}
        if len(hashes) != 1:
            raise ValueError("customers received different block hashes for the same occurrence")
        witnesses.append({"occurrence_id": occurrence, "block_hash": hashes.pop()})
    return {
        "passed": ready and bool(witnesses),
        "rules_loaded": len(loaded),
        "common_block": witnesses[0] if witnesses else None,
        "customers": counts,
        "observed_selected_occurrences": len({record["occurrence_id"] for records in matched.values() for record in records}),
    }


def status_at(address, token):
    request = urllib.request.Request(
        f"http://{address}/v1/config/status", headers={"Authorization": f"Bearer {token}"}
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    with opener.open(request, timeout=2) as response:
        return json.load(response)


def reconcile_routes(root, matched):
    dispatched = []
    submissions = {customer: [] for customer in CUSTOMERS}
    failures = {"input_dropped", "input_canceled", "destination_dropped", "destination_canceled", "plugin_error", "submission_uncertain"}
    for record in json_lines(root / "adder.log", diagnostic=True, complete=True):
        if record.get("component") != "managed.router":
            continue
        action = record.get("action")
        if action in failures:
            raise ValueError(f"router reported {action}, including during shutdown")
        if action == "dispatched":
            dispatched.append(record.get("occurrence_id"))
        if action == "submitted_to_plugin":
            customer = record.get("customer_id")
            if customer not in submissions or record.get("destination_id") != "local-log":
                raise ValueError("unexpected customer or destination submission")
            submissions[customer].append((record.get("occurrence_id"), record.get("revision"), record.get("rule_ids")))
    if dispatched != list(range(1, len(dispatched) + 1)):
        raise ValueError("dispatch occurrence diagnostics are missing or out of order")
    observed = set(dispatched)
    for customer in CUSTOMERS:
        expected = [(record["occurrence_id"], record["revision"], [record["rule_id"]]) for record in matched[customer]]
        if submissions[customer] != expected:
            raise ValueError(f"{customer} plugin submissions do not reconcile with matched rules")
        if not {record["occurrence_id"] for record in matched[customer]}.issubset(observed):
            raise ValueError("a match has no observed source dispatch")
    return {
        "dispatched_occurrences": len(dispatched),
        "plugin_submissions": {customer: len(records) for customer, records in submissions.items()},
        "dropped_or_uncertain_records": 0,
    }


def verify_status(status):
    if not status.get("ready") or status.get("applied_generation") != 1:
        raise ValueError("router did not report a ready initial generation")
    customers = status.get("customers", {})
    if set(customers) != set(CUSTOMERS) or any(value.get("revision") != 1 for value in customers.values()):
        raise ValueError("router status does not show the five applied customer revisions")
    counters = status.get("counters", {})
    for key in ("input_dropped", "destination_dropped", "plugin_errors", "canceled"):
        if counters.get(key, 0) != 0:
            raise ValueError(f"router reported {key} during the live smoke run")


def stop(process):
    if process.poll() is not None:
        return False
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=35)
        return False
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
        return True


def run(args):
    root = args.output_dir.resolve()
    binary = args.adder.resolve(strict=True)
    token = prepare(root)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        address = f"127.0.0.1:{listener.getsockname()[1]}"
    command = [
        str(binary), "--input", "chainsync",
        "--managed-config-dir", str(root / "config"),
        "--managed-credentials-dir", str(root / "credentials"),
        "--managed-log-dir", str(root / "events"),
        "--managed-token-file", str(root / "operator-token"),
        "--managed-address", address,
        "--api-address", "127.0.0.1", "--api-port", "0", "--debug-port", "0",
        "--output-log-level", "debug",
        "--input-chainsync-network", args.network,
        "--input-chainsync-address", args.source_address or "",
        "--input-chainsync-socket-path", "",
        "--input-chainsync-intersect-tip=true",
        "--input-chainsync-intersect-point", "",
        "--input-chainsync-auto-reconnect=true",
        "--input-chainsync-state-path", "",
        "--input-chainsync-delay-confirmations", "0",
    ]
    write_json(root / "run.json", {"source_kind": args.source_kind, "network": args.network, "command": command})
    summary = {"passed": False}
    last_status = None
    forced_stop = False
    failure = None
    started = time.monotonic()
    print(f"Waiting for a shared live block in five customer lanes; artifacts: {root}", flush=True)
    with (root / "adder.log").open("w", encoding="utf-8") as raw_log:
        process = subprocess.Popen(command, stdout=raw_log, stderr=subprocess.STDOUT, cwd=root)
        try:
            while time.monotonic() - started < args.timeout_seconds:
                if process.poll() is not None:
                    raise RuntimeError(f"Adder exited before acceptance with status {process.returncode}; see adder.log")
                try:
                    last_status = status_at(address, token)
                    write_json(root / "status-last.json", last_status)
                except (urllib.error.URLError, TimeoutError, OSError):
                    pass
                summary = verify(*observations(root), network=args.network)
                if summary["passed"] and last_status is not None:
                    verify_status(last_status)
                    break
                time.sleep(0.25)
            else:
                raise TimeoutError("no reconciled shared block before the smoke deadline")
        except (Exception, KeyboardInterrupt) as error:
            failure = type(error).__name__ + ": " + str(error)
        finally:
            forced_stop = stop(process)
    try:
        configured, matched, events = observations(root, complete=True)
        for customer in CUSTOMERS:
            records = [record for record in configured if record["customer_id"] == customer] + matched[customer]
            with (root / "filter-logs" / f"{customer}.jsonl").open("w", encoding="utf-8") as output:
                for record in records:
                    output.write(json.dumps(record, separators=(",", ":")) + "\n")
        summary = verify(configured, matched, events, network=args.network, complete=True)
        summary["route_reconciliation"] = reconcile_routes(root, matched)
        if token in (root / "adder.log").read_text(encoding="utf-8") or token in json.dumps(last_status):
            raise ValueError("operator token appeared in diagnostics or status")
    except (ValueError, OSError) as error:
        failure = str(error)
    if forced_stop:
        failure = "Adder exceeded its graceful shutdown allowance and was killed"
    if process.returncode != 0:
        failure = failure or f"Adder shutdown exit status {process.returncode}"
    summary.update({
        "passed": summary.get("passed", False) and failure is None,
        "source_kind": args.source_kind,
        "network": args.network,
        "elapsed_seconds": round(time.monotonic() - started, 3),
        "adder_exit_code": process.returncode,
        "forced_stop": forced_stop,
        "error": failure,
        "last_status": last_status,
        "last_sampled_dispatch_count": last_status.get("last_dispatched") if last_status else None,
        "last_sampled_outcomes": last_status.get("counters") if last_status else None,
        "accounting_scope": "Adder-observed matches and local log receipts; no claim of complete node history or external provider delivery",
    })
    write_json(root / "summary.json", summary)
    print(json.dumps({key: value for key, value in summary.items() if key != "last_status"}, indent=2))
    return 0 if summary["passed"] else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--adder", type=Path, required=True, help="built Adder binary")
    parser.add_argument("--output-dir", type=Path, required=True, help="new or empty run directory")
    parser.add_argument("--source-address", help="ChainSync host:port, required for a Dingo source")
    parser.add_argument("--network", default="mainnet")
    parser.add_argument("--timeout-seconds", type=float, default=180)
    parser.add_argument("--source-kind", choices=("public-mainnet", "dingo"), default="public-mainnet")
    args = parser.parse_args()
    if args.timeout_seconds <= 0:
        parser.error("--timeout-seconds must be positive")
    if args.source_kind == "public-mainnet" and args.network != "mainnet":
        parser.error("public-mainnet requires --network mainnet")
    if args.source_kind == "dingo" and not args.source_address:
        parser.error("dingo requires an explicit --source-address")
    try:
        return run(args)
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
