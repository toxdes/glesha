"""Check the one-off release prefix migration against a fake S3 client."""

import unittest

from migrate_release_prefix import (
    apply_plan,
    build_plan,
    list_objects,
)


class FakeClient:
    def __init__(self, objects, page_size=2, corrupt=None):
        self.objects = dict(objects)
        self.page_size = page_size
        self.corrupt = corrupt or {}
        self.copied = []

    def list_objects_v2(self, **kwargs):
        prefix = kwargs["Prefix"]
        keys = sorted(key for key in self.objects if key.startswith(prefix))
        start = int(kwargs.get("ContinuationToken") or 0)
        chunk = keys[start : start + self.page_size]
        page = {"Contents": [{"Key": key, "Size": self.objects[key]} for key in chunk]}
        if start + self.page_size < len(keys):
            page["IsTruncated"] = True
            page["NextContinuationToken"] = str(start + self.page_size)
        return page

    def copy_object(self, **kwargs):
        source = kwargs["CopySource"]["Key"]
        self.copied.append((source, kwargs["Key"]))
        size = self.objects[source]
        if kwargs["Key"] in self.corrupt:
            size += self.corrupt[kwargs["Key"]]
        self.objects[kwargs["Key"]] = size


SOURCE = "glesha/releases"
TARGET = "releases"


class ListObjectsTests(unittest.TestCase):
    def test_reads_every_page(self):
        client = FakeClient(
            {
                f"{SOURCE}/glesha_0.5.1_amd64.tar.gz": 10,
                f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 20,
                f"{SOURCE}/glesha_0.5.2_amd64.tar.gz.sha256": 30,
            },
            page_size=2,
        )

        found = list_objects(client, "bucket", SOURCE)

        self.assertEqual(3, len(found))
        self.assertEqual(20, found[f"{SOURCE}/glesha_0.5.2_amd64.tar.gz"])

    def test_ignores_directory_markers(self):
        client = FakeClient({f"{SOURCE}/": 0, f"{SOURCE}/file.tar.gz": 5})

        self.assertEqual([f"{SOURCE}/file.tar.gz"], list(list_objects(client, "b", SOURCE)))


class BuildPlanTests(unittest.TestCase):
    cases = [
        (
            "copies missing keys under the target prefix",
            {f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 10},
            {},
            ["releases/glesha_0.5.2_amd64.tar.gz"],
        ),
        (
            "keeps relative paths below the prefix",
            {"glesha/releases/nested/glesha_0.5.2_amd64.tar.gz": 10},
            {},
            ["releases/nested/glesha_0.5.2_amd64.tar.gz"],
        ),
        (
            "skips locks, markers and the shared checksum index",
            {
                f"{SOURCE}/.direct.lock": 1,
                f"{SOURCE}/.published/glesha/0.5.1.published": 1,
                f"{SOURCE}/SHA256SUMS": 1,
                f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 10,
            },
            {},
            ["releases/glesha_0.5.2_amd64.tar.gz"],
        ),
    ]

    def test_plan(self):
        for name, source, target, expected in self.cases:
            with self.subTest(name):
                plan = build_plan(source, target, SOURCE, TARGET)
                self.assertEqual(expected, [copy[1] for copy in plan.copies])

    def test_matching_object_is_skipped(self):
        source = {f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 10}
        target = {f"{TARGET}/glesha_0.5.2_amd64.tar.gz": 10}

        plan = build_plan(source, target, SOURCE, TARGET)

        self.assertEqual([], plan.copies)
        self.assertEqual([f"{TARGET}/glesha_0.5.2_amd64.tar.gz"], plan.existing)

    def test_different_size_is_a_conflict(self):
        source = {f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 10}
        target = {f"{TARGET}/glesha_0.5.2_amd64.tar.gz": 99}

        plan = build_plan(source, target, SOURCE, TARGET)

        self.assertEqual(
            [(f"{TARGET}/glesha_0.5.2_amd64.tar.gz", 99, 10)], plan.conflicts
        )

    def test_identical_prefixes_are_rejected(self):
        with self.assertRaises(ValueError):
            build_plan({}, {}, SOURCE, SOURCE)


class ApplyPlanTests(unittest.TestCase):
    def setUp(self):
        self.source = {
            f"{SOURCE}/glesha_0.5.1_amd64.tar.gz": 10,
            f"{SOURCE}/glesha_0.5.2_amd64.tar.gz": 20,
        }

    def test_copies_only_missing_objects(self):
        client = FakeClient(self.source)
        plan = build_plan(
            list_objects(client, "bucket", SOURCE), {}, SOURCE, TARGET
        )

        apply_plan(client, "bucket", plan, TARGET)

        self.assertEqual(2, len(client.copied))
        self.assertEqual(
            {f"{TARGET}/glesha_0.5.1_amd64.tar.gz": 10,
             f"{TARGET}/glesha_0.5.2_amd64.tar.gz": 20},
            {key: size for key, size in client.objects.items() if key.startswith(f"{TARGET}/")},
        )

    def test_second_run_copies_nothing(self):
        client = FakeClient(self.source)
        first = build_plan(list_objects(client, "bucket", SOURCE), {}, SOURCE, TARGET)
        apply_plan(client, "bucket", first, TARGET)

        second = build_plan(
            list_objects(client, "bucket", SOURCE),
            list_objects(client, "bucket", TARGET),
            SOURCE,
            TARGET,
        )

        self.assertEqual([], second.copies)
        self.assertEqual(2, len(second.existing))

    def test_verification_catches_a_bad_copy(self):
        client = FakeClient(
            self.source, corrupt={f"{TARGET}/glesha_0.5.1_amd64.tar.gz": 1}
        )
        plan = build_plan(list_objects(client, "bucket", SOURCE), {}, SOURCE, TARGET)

        with self.assertRaises(ValueError):
            apply_plan(client, "bucket", plan, TARGET)


if __name__ == "__main__":
    unittest.main()
