"""Data assertions executed inside Kubernetes; preserve the collection across jobs."""
import argparse
import random
import json
import time

from pymilvus import Collection, CollectionSchema, DataType, FieldSchema, connections, utility

BATCH = 512
DIM = 32
NAME = "lifecycle_data"


def vector(pk):
    rng = random.Random(pk)
    return [rng.random() for _ in range(DIM)]


def verify(collection, batches, timeout=120):
    # Check every persisted primary key and scalar value, not only entity count.
    for batch in range(batches):
        ids = list(range(batch * BATCH, (batch + 1) * BATCH))
        rows = collection.query(expr=f"id in {ids}", output_fields=["id", "value"],
                                consistency_level="Strong", timeout=timeout)
        actual = {row["id"]: row["value"] for row in rows}
        expected = {pk: pk * 7 for pk in ids}
        if len(rows) != BATCH or actual != expected:
            raise AssertionError(f"batch {batch}: persisted data mismatch")
    probes = [0, batches * BATCH - 1]
    results = collection.search([vector(pk) for pk in probes], "vector",
                                {"metric_type": "L2", "params": {}}, limit=1,
                                consistency_level="Strong", timeout=timeout)
    if len(results) != len(probes):
        raise AssertionError("missing search results")
    for pk, hits in zip(probes, results):
        if len(hits) != 1 or hits[0].id != pk or hits[0].distance > 0.0001:
            raise AssertionError(f"search failed for primary key {pk}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("stage", type=int)
    parser.add_argument("--host", required=True)
    parser.add_argument("--probe", action="store_true")
    args = parser.parse_args()
    connections.connect(host=args.host, port="19530", timeout=120)
    if args.probe:
        collection = Collection(NAME)
        successes = failures = 0
        outage_start = None
        max_outage = 0.0
        while True:
            error = None
            try:
                verify(collection, 1, timeout=10)
                successes += 1
                if outage_start is not None:
                    max_outage = max(max_outage, time.monotonic() - outage_start)
                outage_start = None
            except Exception as exc:
                failures += 1
                error = str(exc)
                if outage_start is None:
                    outage_start = time.monotonic()
            ongoing = time.monotonic() - outage_start if outage_start is not None else 0
            print(json.dumps({"time": time.time(), "successes": successes, "failures": failures,
                              "max_observed_outage_seconds": max(max_outage, ongoing),
                              "error": error}), flush=True)
            time.sleep(2)
    if args.stage == 0:
        if utility.has_collection(NAME):
            raise AssertionError("seed collection already exists")
        collection = Collection(NAME, CollectionSchema([
            FieldSchema("id", DataType.INT64, is_primary=True, auto_id=False),
            FieldSchema("value", DataType.INT64),
            FieldSchema("vector", DataType.FLOAT_VECTOR, dim=DIM),
        ]), consistency_level="Strong")
    else:
        if not utility.has_collection(NAME):
            raise AssertionError("original collection is missing")
        collection = Collection(NAME)
        # Do not reload after an operation: QueryNode movement must preserve load.
        verify(collection, args.stage)
    ids = list(range(args.stage * BATCH, (args.stage + 1) * BATCH))
    result = collection.insert([ids, [pk * 7 for pk in ids], [vector(pk) for pk in ids]])
    if list(result.primary_keys) != ids:
        raise AssertionError("insert primary keys mismatch")
    collection.flush(timeout=120)
    if args.stage == 0:
        collection.create_index("vector", {"index_type": "FLAT", "metric_type": "L2", "params": {}},
                                timeout=180)
        collection.load(timeout=180)
    verify(collection, args.stage + 1)
    print(f"PASS stage={args.stage} rows={(args.stage + 1) * BATCH}", flush=True)


if __name__ == "__main__":
    main()
