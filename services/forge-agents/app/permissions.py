"""Permission scope model and checker for tool calls."""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Iterable, Sequence


@dataclass(frozen=True)
class CallScope:
    """Effective identity/project scope for a single tool invocation."""

    project_id: str
    permissions: frozenset[str] = field(default_factory=frozenset)

    @classmethod
    def from_permissions(
        cls,
        permissions: Iterable[str],
        *,
        project_id: str = "default",
    ) -> CallScope:
        cleaned = frozenset(p.strip() for p in permissions if isinstance(p, str) and p.strip())
        return cls(project_id=project_id, permissions=cleaned)


class PermissionChecker:
    """Deny-by-default matcher of required permissions against a call scope."""

    def has_permission(self, scope: CallScope, required: Sequence[str]) -> bool:
        """True when every required permission is present in the scope."""
        if not required:
            return True
        granted = scope.permissions
        return all(perm in granted for perm in required)

    def missing_permissions(self, scope: CallScope, required: Sequence[str]) -> list[str]:
        """Return required permissions that are absent from the scope."""
        granted = scope.permissions
        return [perm for perm in required if perm not in granted]
