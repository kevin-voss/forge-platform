"""Unit tests for PermissionChecker grant/deny matrix."""

from __future__ import annotations

import pytest

from app.permissions import CallScope, PermissionChecker


@pytest.fixture
def checker() -> PermissionChecker:
    return PermissionChecker()


def test_empty_required_always_granted(checker: PermissionChecker) -> None:
    scope = CallScope.from_permissions([])
    assert checker.has_permission(scope, []) is True


def test_grant_when_all_present(checker: PermissionChecker) -> None:
    scope = CallScope.from_permissions(["project:read", "deployment:read"])
    assert checker.has_permission(scope, ["project:read"]) is True
    assert checker.has_permission(scope, ["project:read", "deployment:read"]) is True


def test_deny_when_missing(checker: PermissionChecker) -> None:
    scope = CallScope.from_permissions(["project:read"])
    assert checker.has_permission(scope, ["deployment:read"]) is False
    assert checker.missing_permissions(scope, ["deployment:read"]) == ["deployment:read"]


def test_deny_partial_set(checker: PermissionChecker) -> None:
    scope = CallScope.from_permissions(["project:read"])
    required = ["project:read", "runtime:restart"]
    assert checker.has_permission(scope, required) is False
    assert checker.missing_permissions(scope, required) == ["runtime:restart"]


def test_empty_scope_denies_nonempty_required(checker: PermissionChecker) -> None:
    scope = CallScope.from_permissions([])
    assert checker.has_permission(scope, ["project:read"]) is False
