"""Model backend adapters."""

from app.adapters.base import Capability, HealthStatus, ModelAdapter
from app.adapters.fake import FakeAdapter

__all__ = ["Capability", "FakeAdapter", "HealthStatus", "ModelAdapter"]
