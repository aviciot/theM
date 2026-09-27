-- docs/APPFLOW_TEXT_GUARDS_PLAN.md Phase 4: an llm canvas node has no agent
-- at all (its provider/model are configured in the app's Runtime tab, not
-- tied to a them.agents row) -- but them.middleware_wirings.agent_id was
-- NOT NULL with a real FK, so an llm node could never get a guard wiring
-- of any kind. node_id (added in an earlier phase, docs/
-- APPFLOW_A2A_RESPONSE_KINDS_PLAN.md Phase 2) is already the real identity
-- every canvas-created wiring uses to scope a guard to one specific node
-- instance -- agent_id becomes optional, node_id is the identity going
-- forward for both agent and llm node wirings.

ALTER TABLE them.middleware_wirings
  ALTER COLUMN agent_id DROP NOT NULL;

-- uq_mw_wiring_app_agent_pos (application_id, agent_id, position) stops
-- being a useful uniqueness guarantee for agent_id IS NULL rows (NULL is
-- never equal to NULL in a unique constraint, so it silently allows
-- unlimited llm-node wirings at the same position) -- but every real
-- canvas wiring already carries a node_id (uq_mw_wiring_app_node), which is
-- the identity that actually matters. Left in place, not dropped: it still
-- correctly protects agent-node wirings created before node_id existed.
