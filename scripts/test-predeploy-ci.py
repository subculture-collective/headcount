#!/usr/bin/env python3
"""Regression checks for fail-closed private-context admission."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('predeploy', Path(__file__).with_name('predeploy-ci.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class AdmissionTests(unittest.TestCase):
    def metadata(self):
        data = {key: False for key in (
            'superuser', 'create_role', 'create_database', 'replication', 'bypass_rls',
            'database_create', 'private_observations_select', 'private_snapshots_select',
            'private_contexts_select', 'private_context_references_select')}
        data.update({key: 0 for key in (
            'writable_schemas', 'writable_tables', 'writable_sequences', 'security_definer_functions')})
        data.update(role='susq_api', login=True, read_only_default=True, applied_migrations=['fixture.sql'])
        return data

    def test_private_tables_and_migration_admission(self):
        module.admit(self.metadata(), ['fixture.sql'])
        for key in ('private_contexts_select', 'private_context_references_select'):
            with self.subTest(privilege=key):
                data = self.metadata()
                data[key] = True
                with self.assertRaises(AssertionError):
                    module.admit(data, ['fixture.sql'])
        with self.assertRaises(AssertionError):
            module.admit(self.metadata(), ['unadmitted.sql'])


if __name__ == '__main__':
    unittest.main()
