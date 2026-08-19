-- Migration 037: extend agents.transport CHECK to include canvas_a2a
-- Canvas-generated agents are stored with transport='canvas_a2a' to distinguish
-- them from external A2A agents. The runtime reads the spec from agent_runtime_specs
-- rather than calling an external endpoint.

ALTER TABLE them.agents DROP CONSTRAINT IF EXISTS agents_transport_check;
ALTER TABLE them.agents ADD CONSTRAINT agents_transport_check
    CHECK (transport = ANY (ARRAY['a2a_async'::text, 'canvas_a2a'::text]));
