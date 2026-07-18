"""
Admin — Monitoring Configuration
Stores heatmap thresholds + display settings in them.config["monitoring"].

GET  /api/v1/admin/monitoring-config
PUT  /api/v1/admin/monitoring-config
"""

from typing import Any, Dict

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field, model_validator
from sqlalchemy.ext.asyncio import AsyncSession

from app._deps import get_db
from app.models import Config

router = APIRouter(prefix="/admin/monitoring-config", tags=["admin-monitoring"])

_CONFIG_KEY = "monitoring"

_DEFAULTS: Dict[str, Any] = {
    "heatmap_low":         1,
    "heatmap_medium":      10,
    "heatmap_high":        50,
    "edge_thin":           1,
    "edge_medium":         10,
    "edge_thick":          50,
    "panel_max_sessions":  50,
    "stats_window_seconds": 300,
}


class MonitoringConfig(BaseModel):
    heatmap_low:           int = Field(1,   gt=0, le=10000)
    heatmap_medium:        int = Field(10,  gt=0, le=10000)
    heatmap_high:          int = Field(50,  gt=0, le=10000)
    edge_thin:             int = Field(1,   gt=0, le=10000)
    edge_medium:           int = Field(10,  gt=0, le=10000)
    edge_thick:            int = Field(50,  gt=0, le=10000)
    panel_max_sessions:    int = Field(50,  gt=0, le=10000)
    stats_window_seconds:  int = Field(300, gt=0, le=86400)

    @model_validator(mode='after')
    def check_threshold_ordering(self) -> 'MonitoringConfig':
        if not (self.heatmap_low < self.heatmap_medium < self.heatmap_high):
            raise ValueError("heatmap thresholds must satisfy low < medium < high")
        if not (self.edge_thin < self.edge_medium < self.edge_thick):
            raise ValueError("edge thresholds must satisfy thin < medium < thick")
        return self


def _load(row: Config | None) -> Dict[str, Any]:
    if row is None or not row.config_value:
        return dict(_DEFAULTS)
    merged = dict(_DEFAULTS)
    merged.update(row.config_value)
    return merged


@router.get("", response_model=MonitoringConfig)
async def get_monitoring_config(
    db: AsyncSession = Depends(get_db),
) -> MonitoringConfig:
    row = await db.get(Config, _CONFIG_KEY)
    return MonitoringConfig(**_load(row))


@router.put("", response_model=MonitoringConfig)
async def put_monitoring_config(
    body: MonitoringConfig,
    db: AsyncSession = Depends(get_db),
) -> MonitoringConfig:
    row = await db.get(Config, _CONFIG_KEY)
    data = body.model_dump()
    if row is None:
        db.add(Config(config_key=_CONFIG_KEY, config_value=data))
    else:
        row.config_value = data
    await db.commit()
    return body
