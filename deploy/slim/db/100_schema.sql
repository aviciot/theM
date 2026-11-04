--
-- PostgreSQL database dump
--


-- Dumped from database version 16.11
-- Dumped by pg_dump version 16.11

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: auth_service; Type: SCHEMA; Schema: -; Owner: them
--

CREATE SCHEMA auth_service;


ALTER SCHEMA auth_service OWNER TO them;

--
-- Name: them; Type: SCHEMA; Schema: -; Owner: them
--

CREATE SCHEMA them;


ALTER SCHEMA them OWNER TO them;

--
-- Name: enqueue_artifact_object_deletion(); Type: FUNCTION; Schema: them; Owner: them
--

CREATE FUNCTION them.enqueue_artifact_object_deletion() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    SET search_path TO 'them', 'pg_temp'
    AS $$
BEGIN
    IF OLD.storage_key IS NOT NULL AND OLD.storage_key <> '' THEN
        INSERT INTO them.storage_deletions (tenant_id, bucket, storage_key)
        VALUES (OLD.tenant_id, 'artifacts', OLD.storage_key);
    END IF;
    RETURN OLD;
END
$$;


ALTER FUNCTION them.enqueue_artifact_object_deletion() OWNER TO them;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: auth_audit; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.auth_audit (
    id integer NOT NULL,
    user_id integer,
    username character varying(255),
    action character varying(100) NOT NULL,
    status character varying(50) NOT NULL,
    ip_address character varying(45),
    user_agent text,
    details text,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.auth_audit OWNER TO them;

--
-- Name: auth_audit_id_seq; Type: SEQUENCE; Schema: auth_service; Owner: them
--

CREATE SEQUENCE auth_service.auth_audit_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE auth_service.auth_audit_id_seq OWNER TO them;

--
-- Name: auth_audit_id_seq; Type: SEQUENCE OWNED BY; Schema: auth_service; Owner: them
--

ALTER SEQUENCE auth_service.auth_audit_id_seq OWNED BY auth_service.auth_audit.id;


--
-- Name: blacklisted_tokens; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.blacklisted_tokens (
    token_hash character varying(255) NOT NULL,
    blacklisted_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamp without time zone NOT NULL
);


ALTER TABLE auth_service.blacklisted_tokens OWNER TO them;

--
-- Name: roles; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.roles (
    id integer NOT NULL,
    name character varying(50) NOT NULL,
    description text,
    mcp_access text[] DEFAULT '{}'::text[],
    tool_restrictions jsonb DEFAULT '{}'::jsonb,
    dashboard_access character varying(20) DEFAULT 'none'::character varying,
    rate_limit integer DEFAULT 1000,
    cost_limit_daily numeric(10,2) DEFAULT 100.00,
    token_expiry integer DEFAULT 3600,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.roles OWNER TO them;

--
-- Name: roles_id_seq; Type: SEQUENCE; Schema: auth_service; Owner: them
--

CREATE SEQUENCE auth_service.roles_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE auth_service.roles_id_seq OWNER TO them;

--
-- Name: roles_id_seq; Type: SEQUENCE OWNED BY; Schema: auth_service; Owner: them
--

ALTER SEQUENCE auth_service.roles_id_seq OWNED BY auth_service.roles.id;


--
-- Name: team_members; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.team_members (
    team_id integer NOT NULL,
    user_id integer NOT NULL,
    role character varying(50) DEFAULT 'member'::character varying,
    joined_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.team_members OWNER TO them;

--
-- Name: teams; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.teams (
    id integer NOT NULL,
    name character varying(100) NOT NULL,
    description text,
    mcp_access text[] DEFAULT '{}'::text[],
    resource_access jsonb DEFAULT '{}'::jsonb,
    team_rate_limit integer,
    team_cost_limit numeric(10,2),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.teams OWNER TO them;

--
-- Name: teams_id_seq; Type: SEQUENCE; Schema: auth_service; Owner: them
--

CREATE SEQUENCE auth_service.teams_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE auth_service.teams_id_seq OWNER TO them;

--
-- Name: teams_id_seq; Type: SEQUENCE OWNED BY; Schema: auth_service; Owner: them
--

ALTER SEQUENCE auth_service.teams_id_seq OWNED BY auth_service.teams.id;


--
-- Name: tenant_memberships; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.tenant_memberships (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id integer NOT NULL,
    tenant_id uuid NOT NULL,
    role text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE auth_service.tenant_memberships OWNER TO them;

--
-- Name: user_overrides; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.user_overrides (
    user_id integer NOT NULL,
    mcp_restrictions text[] DEFAULT '{}'::text[],
    tool_restrictions jsonb DEFAULT '{}'::jsonb,
    custom_rate_limit integer,
    custom_cost_limit numeric(10,2),
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.user_overrides OWNER TO them;

--
-- Name: user_sessions; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.user_sessions (
    id integer NOT NULL,
    user_id integer,
    access_token_hash character varying(255) NOT NULL,
    refresh_token_hash character varying(255) NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP
);


ALTER TABLE auth_service.user_sessions OWNER TO them;

--
-- Name: user_sessions_id_seq; Type: SEQUENCE; Schema: auth_service; Owner: them
--

CREATE SEQUENCE auth_service.user_sessions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE auth_service.user_sessions_id_seq OWNER TO them;

--
-- Name: user_sessions_id_seq; Type: SEQUENCE OWNED BY; Schema: auth_service; Owner: them
--

ALTER SEQUENCE auth_service.user_sessions_id_seq OWNED BY auth_service.user_sessions.id;


--
-- Name: users; Type: TABLE; Schema: auth_service; Owner: them
--

CREATE TABLE auth_service.users (
    id integer NOT NULL,
    username character varying(100) NOT NULL,
    name character varying(255) NOT NULL,
    email character varying(255),
    role_id integer,
    password_hash character varying(255),
    api_key_hash character varying(255),
    active boolean DEFAULT true NOT NULL,
    rate_limit_override integer,
    last_login_at timestamp without time zone,
    created_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp without time zone DEFAULT CURRENT_TIMESTAMP,
    preferences jsonb DEFAULT '{}'::jsonb NOT NULL,
    idp_sub text,
    idp_issuer text
);


ALTER TABLE auth_service.users OWNER TO them;

--
-- Name: users_id_seq; Type: SEQUENCE; Schema: auth_service; Owner: them
--

CREATE SEQUENCE auth_service.users_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE auth_service.users_id_seq OWNER TO them;

--
-- Name: users_id_seq; Type: SEQUENCE OWNED BY; Schema: auth_service; Owner: them
--

ALTER SEQUENCE auth_service.users_id_seq OWNED BY auth_service.users.id;


--
-- Name: access_tokens; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.access_tokens (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    token_hash text NOT NULL,
    user_id integer,
    orchestrator_id uuid,
    expires_at timestamp with time zone,
    last_used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    label text DEFAULT 'default'::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    is_backend boolean DEFAULT false NOT NULL
);

ALTER TABLE ONLY them.access_tokens FORCE ROW LEVEL SECURITY;


ALTER TABLE them.access_tokens OWNER TO them_owner;

--
-- Name: COLUMN access_tokens.is_backend; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.access_tokens.is_backend IS 'When true, the bearer of this token is a trusted server-side backend and may assert end-user identity via the X-External-User header. Must not be set on tokens issued to mobile or browser clients.';


--
-- Name: agent_definitions; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.agent_definitions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    agent_slug text NOT NULL,
    revision integer NOT NULL,
    definition jsonb NOT NULL,
    definition_hash text NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner_id integer,
    display_name text GENERATED ALWAYS AS (((definition -> 'agent_root'::text) ->> 'display_name'::text)) STORED,
    CONSTRAINT agent_definitions_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text])))
);

ALTER TABLE ONLY them.agent_definitions FORCE ROW LEVEL SECURITY;


ALTER TABLE them.agent_definitions OWNER TO them_owner;

--
-- Name: agent_runtime_specs; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.agent_runtime_specs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    definition_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    spec jsonb NOT NULL,
    spec_hash text NOT NULL,
    deployed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.agent_runtime_specs FORCE ROW LEVEL SECURITY;


ALTER TABLE them.agent_runtime_specs OWNER TO them_owner;

--
-- Name: agents; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.agents (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    transport text DEFAULT 'a2a_async'::text NOT NULL,
    endpoint_url text,
    auth_token_encrypted text,
    input_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    timeout_seconds integer DEFAULT 30 NOT NULL,
    max_concurrency integer DEFAULT 5 NOT NULL,
    max_retries integer DEFAULT 2 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    agent_card jsonb,
    agent_card_url text,
    card_fetched_at timestamp with time zone,
    skills jsonb DEFAULT '[]'::jsonb NOT NULL,
    supports_streaming boolean DEFAULT false NOT NULL,
    supports_push boolean DEFAULT false NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    icon text,
    category text,
    last_scan_at timestamp with time zone,
    last_scan_result jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    namespace text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    scope text DEFAULT 'tenant'::text NOT NULL,
    status text DEFAULT 'published'::text NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    created_by integer,
    CONSTRAINT agents_slug_check CHECK ((slug ~ '^[a-z0-9_]{1,48}$'::text)),
    CONSTRAINT agents_transport_check CHECK ((transport = ANY (ARRAY['a2a_async'::text, 'canvas_a2a'::text]))),
    CONSTRAINT ck_agents_scope CHECK ((scope = ANY (ARRAY['builtin'::text, 'tenant'::text]))),
    CONSTRAINT ck_agents_status CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text, 'deprecated'::text])))
);

ALTER TABLE ONLY them.agents FORCE ROW LEVEL SECURITY;


ALTER TABLE them.agents OWNER TO them_owner;

--
-- Name: app_agent_bindings; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.app_agent_bindings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    definition_id uuid,
    credential_bindings jsonb DEFAULT '{}'::jsonb NOT NULL,
    config_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    policies jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    agent_params jsonb DEFAULT '{}'::jsonb NOT NULL
);

ALTER TABLE ONLY them.app_agent_bindings FORCE ROW LEVEL SECURITY;


ALTER TABLE them.app_agent_bindings OWNER TO them_owner;

--
-- Name: COLUMN app_agent_bindings.agent_params; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.app_agent_bindings.agent_params IS 'Runtime parameters for this agent binding. Secrets encrypted via Fernet (enc: prefix). Format: {key: {ct: "enc:...", hint: "XXXX"}} for secrets,         {key: "value"} for non-secrets.';


--
-- Name: app_debug_config; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.app_debug_config (
    application_id uuid NOT NULL,
    log_verbosity text DEFAULT 'status'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT app_debug_config_log_verbosity_check CHECK ((log_verbosity = ANY (ARRAY['off'::text, 'status'::text, 'full'::text])))
);


ALTER TABLE them.app_debug_config OWNER TO them;

--
-- Name: app_flow_http_params; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.app_flow_http_params (
    application_id uuid NOT NULL,
    node_id text NOT NULL,
    param_key text NOT NULL,
    value_encrypted text,
    inject_mode text DEFAULT 'header'::text NOT NULL,
    inject_header_name text,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.app_flow_http_params FORCE ROW LEVEL SECURITY;


ALTER TABLE them.app_flow_http_params OWNER TO them;

--
-- Name: app_flow_llm_overrides; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.app_flow_llm_overrides (
    application_id uuid NOT NULL,
    node_id text NOT NULL,
    provider text NOT NULL,
    model text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE them.app_flow_llm_overrides OWNER TO them;

--
-- Name: app_mcp_credentials; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.app_mcp_credentials (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    mcp_server_id uuid NOT NULL,
    credential_encrypted text,
    auth_header_name text DEFAULT 'Authorization'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.app_mcp_credentials FORCE ROW LEVEL SECURITY;


ALTER TABLE them.app_mcp_credentials OWNER TO them_owner;

--
-- Name: TABLE app_mcp_credentials; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON TABLE them.app_mcp_credentials IS 'Per-application MCP server credentials. Fernet-encrypted at rest (same key as llm_providers). Plaintext is never logged or returned in API responses.';


--
-- Name: app_orchestrators; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.app_orchestrators (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    orchestrator_id uuid,
    name text NOT NULL,
    node_id text NOT NULL,
    kind text DEFAULT 'standard'::text NOT NULL,
    delegatable boolean DEFAULT false NOT NULL,
    display_name text,
    system_prompt text,
    allowed_agent_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    llm_provider text,
    llm_model text,
    llm_api_key_encrypted text,
    llm_base_url text,
    max_iterations integer DEFAULT 10 NOT NULL,
    max_parallel_tools integer DEFAULT 3 NOT NULL,
    rate_limit_rpm integer,
    daily_budget_usd numeric(10,4),
    voice_enabled boolean DEFAULT false NOT NULL,
    transcription_provider character varying(32),
    transcription_model character varying(64),
    transcription_api_key_encrypted text,
    tts_enabled boolean DEFAULT false NOT NULL,
    tts_provider text,
    tts_voice text,
    tts_api_key_encrypted text,
    memory_enabled boolean DEFAULT false NOT NULL,
    summarize_every_n_calls integer DEFAULT 3 NOT NULL,
    memory_raw_fallback_n integer DEFAULT 5 NOT NULL,
    summarizer_provider text,
    summarizer_model text,
    summarizer_api_key_encrypted text,
    edges text[] DEFAULT '{websocket}'::text[] NOT NULL,
    history_window integer DEFAULT 20 NOT NULL,
    budget_tokens integer,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    component_definition_id uuid,
    component_version integer,
    source_definition_id uuid,
    source_definition_hash text,
    mcp_servers jsonb DEFAULT '[]'::jsonb NOT NULL,
    CONSTRAINT app_orchestrators_kind_check CHECK ((kind = ANY (ARRAY['standard'::text, 'router'::text, 'voice'::text]))),
    CONSTRAINT app_orchestrators_name_check CHECK ((name ~ '^[a-z0-9_-]{1,64}$'::text))
);

ALTER TABLE ONLY them.app_orchestrators FORCE ROW LEVEL SECURITY;


ALTER TABLE them.app_orchestrators OWNER TO them_owner;

--
-- Name: app_temporal_config; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.app_temporal_config (
    application_id uuid NOT NULL,
    max_concurrent_workflows integer,
    workflow_timeout_s integer,
    activity_timeout_s integer,
    retry_max_attempts integer,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE them.app_temporal_config OWNER TO them;

--
-- Name: appflow_debug_presets; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.appflow_debug_presets (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    user_id bigint NOT NULL,
    application_id uuid NOT NULL,
    name text NOT NULL,
    entry_point_slug text NOT NULL,
    user_message text DEFAULT ''::text NOT NULL,
    step_mode boolean DEFAULT false NOT NULL,
    llm_overrides jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.appflow_debug_presets FORCE ROW LEVEL SECURITY;


ALTER TABLE them.appflow_debug_presets OWNER TO them_owner;

--
-- Name: application_definitions; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.application_definitions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    revision integer NOT NULL,
    status text NOT NULL,
    definition jsonb NOT NULL,
    definition_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    CONSTRAINT application_definitions_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text])))
);

ALTER TABLE ONLY them.application_definitions FORCE ROW LEVEL SECURITY;


ALTER TABLE them.application_definitions OWNER TO them_owner;

--
-- Name: applications; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.applications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    presentation jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    conversation_token_limit integer,
    runtime_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    canvas jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    active_definition_id uuid,
    provider_keys jsonb DEFAULT '{}'::jsonb NOT NULL,
    app_params jsonb DEFAULT '{}'::jsonb NOT NULL,
    slug text NOT NULL,
    security_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT applications_slug_check CHECK ((slug ~ '^[a-z0-9][a-z0-9_-]{0,47}$'::text))
);

ALTER TABLE ONLY them.applications FORCE ROW LEVEL SECURITY;


ALTER TABLE them.applications OWNER TO them_owner;

--
-- Name: COLUMN applications.runtime_config; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.applications.runtime_config IS 'App-level runtime policy: {max_concurrent_sessions, rate_limit_rpm, blocked_tokens[], blocked_user_ids[], session_timeout_minutes}. {} = unlimited.';


--
-- Name: COLUMN applications.app_params; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.applications.app_params IS 'App-level global named parameters. Secrets: {"name": {"ct": "enc:...", "hint": "XXXX"}}. Non-secrets: {"name": "value"}. Encrypted with the platform crypto key.';


--
-- Name: artifacts; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.artifacts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    context_id uuid,
    artifact_id text NOT NULL,
    name text,
    parts jsonb,
    append_index integer,
    last_chunk boolean,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.artifacts FORCE ROW LEVEL SECURITY;


ALTER TABLE them.artifacts OWNER TO them_owner;

--
-- Name: audit_logs; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.audit_logs (
    id bigint NOT NULL,
    user_id integer,
    action text NOT NULL,
    entity_type text,
    entity_id text,
    details jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid
);

ALTER TABLE ONLY them.audit_logs FORCE ROW LEVEL SECURITY;


ALTER TABLE them.audit_logs OWNER TO them_owner;

--
-- Name: audit_logs_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.audit_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.audit_logs_id_seq OWNER TO them_owner;

--
-- Name: audit_logs_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.audit_logs_id_seq OWNED BY them.audit_logs.id;


--
-- Name: component_definitions; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.component_definitions (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    kind text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    version integer NOT NULL,
    display_name text NOT NULL,
    description text,
    implementation_type text NOT NULL,
    configuration_schema jsonb DEFAULT '{}'::jsonb NOT NULL,
    default_config jsonb DEFAULT '{}'::jsonb NOT NULL,
    capabilities jsonb DEFAULT '[]'::jsonb NOT NULL,
    input_schema jsonb,
    output_schema jsonb,
    credential_schema jsonb DEFAULT '[]'::jsonb NOT NULL,
    scope text NOT NULL,
    tenant_id uuid,
    status text NOT NULL,
    content_hash text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    published_at timestamp with time zone,
    CONSTRAINT component_definitions_kind_check CHECK ((kind = ANY (ARRAY['agent'::text, 'orchestrator'::text, 'middleware'::text, 'tool'::text, 'entry_point'::text]))),
    CONSTRAINT component_definitions_scope_check CHECK ((scope = ANY (ARRAY['builtin'::text, 'tenant'::text]))),
    CONSTRAINT component_definitions_status_check CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text, 'deprecated'::text])))
);

ALTER TABLE ONLY them.component_definitions FORCE ROW LEVEL SECURITY;


ALTER TABLE them.component_definitions OWNER TO them_owner;

--
-- Name: config; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.config (
    config_key text NOT NULL,
    config_value jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE them.config OWNER TO them_owner;

--
-- Name: entry_points; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.entry_points (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    app_orchestrator_id uuid,
    slug text NOT NULL,
    entry_point_type text NOT NULL,
    access_policy jsonb DEFAULT '{"mode": "token"}'::jsonb NOT NULL,
    conversation_token_limit integer,
    max_concurrent_sessions integer,
    queue_timeout_seconds integer,
    queue_message text,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    source_definition_id uuid,
    source_definition_hash text,
    memory_enabled boolean DEFAULT false NOT NULL,
    history_window integer DEFAULT 20 NOT NULL,
    summarize_every_n_calls integer DEFAULT 10 NOT NULL,
    memory_raw_fallback_n integer DEFAULT 3 NOT NULL,
    summarizer_provider text,
    summarizer_model text,
    llm_provider text,
    llm_model text,
    agent_card jsonb,
    card_synthesized_at timestamp with time zone,
    allowed_principals text DEFAULT 'internal'::text NOT NULL,
    required_role text,
    CONSTRAINT entry_points_allowed_principals_check CHECK ((allowed_principals = ANY (ARRAY['internal'::text, 'external'::text, 'both'::text]))),
    CONSTRAINT entry_points_entry_point_type_check CHECK ((entry_point_type = ANY (ARRAY['websocket'::text, 'sse'::text, 'webrtc'::text, 'a2a'::text, 'voice'::text, 'gateway'::text]))),
    CONSTRAINT entry_points_slug_check CHECK ((slug ~ '^[a-z0-9_-]{1,64}$'::text))
);

ALTER TABLE ONLY them.entry_points FORCE ROW LEVEL SECURITY;


ALTER TABLE them.entry_points OWNER TO them_owner;

--
-- Name: COLUMN entry_points.max_concurrent_sessions; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.entry_points.max_concurrent_sessions IS 'Max simultaneous active sessions for this entry point. NULL = unlimited.';


--
-- Name: COLUMN entry_points.queue_timeout_seconds; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.entry_points.queue_timeout_seconds IS 'Seconds to wait for a slot before rejecting. NULL = immediate reject (no queue).';


--
-- Name: COLUMN entry_points.queue_message; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.entry_points.queue_message IS 'Message sent to client while waiting for a slot. NULL = default.';


--
-- Name: COLUMN entry_points.required_role; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.entry_points.required_role IS 'Minimum platform role required to access this Entry Point (from auth_service.roles). NULL = no role requirement (any authenticated user for user_jwt mode, or token-based access for token mode).';


--
-- Name: gateway_clients; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.gateway_clients (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    token_hash text NOT NULL,
    label text NOT NULL,
    profile_id uuid,
    last_seen timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    app_id uuid,
    config_status text DEFAULT 'unknown'::text,
    config_status_reason text,
    governance_profile_id uuid,
    CONSTRAINT chk_config_status CHECK ((config_status = ANY (ARRAY['valid'::text, 'invalid_no_policy'::text, 'invalid_profile_without_policy'::text, 'unknown'::text])))
);

ALTER TABLE ONLY them.gateway_clients FORCE ROW LEVEL SECURITY;


ALTER TABLE them.gateway_clients OWNER TO them;

--
-- Name: gateway_profile_steps; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.gateway_profile_steps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    profile_id uuid NOT NULL,
    def_id uuid NOT NULL,
    "position" integer NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL
);


ALTER TABLE them.gateway_profile_steps OWNER TO them;

--
-- Name: gateway_profiles; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.gateway_profiles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.gateway_profiles FORCE ROW LEVEL SECURITY;


ALTER TABLE them.gateway_profiles OWNER TO them;

--
-- Name: gateway_request_bodies; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.gateway_request_bodies (
    request_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    request_text text,
    response_text text,
    retain_until timestamp with time zone DEFAULT (now() + '7 days'::interval) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.gateway_request_bodies FORCE ROW LEVEL SECURITY;


ALTER TABLE them.gateway_request_bodies OWNER TO them;

--
-- Name: gateway_requests; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.gateway_requests (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    client_id uuid,
    token_hash text,
    provider text,
    model_requested text,
    model_served text,
    status text DEFAULT 'ok'::text NOT NULL,
    http_status integer,
    error_code text,
    tokens_in integer DEFAULT 0 NOT NULL,
    tokens_out integer DEFAULT 0 NOT NULL,
    cost_usd numeric(12,8) DEFAULT 0 NOT NULL,
    latency_ms integer,
    ttfb_ms integer,
    streamed boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    user_id integer,
    run_id uuid
);

ALTER TABLE ONLY them.gateway_requests FORCE ROW LEVEL SECURITY;


ALTER TABLE them.gateway_requests OWNER TO them;

--
-- Name: COLUMN gateway_requests.user_id; Type: COMMENT; Schema: them; Owner: them
--

COMMENT ON COLUMN them.gateway_requests.user_id IS 'the-M user ID from JWT (user_jwt mode); NULL for token-based auth';


--
-- Name: COLUMN gateway_requests.run_id; Type: COMMENT; Schema: them; Owner: them
--

COMMENT ON COLUMN them.gateway_requests.run_id IS 'AppFlow run started for this call (profile app); NULL on the direct path';


--
-- Name: governance_policies; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.governance_policies (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    scope_type text NOT NULL,
    scope_id text NOT NULL,
    name text,
    doc jsonb DEFAULT '{"schema_version": 1}'::jsonb NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by text,
    CONSTRAINT governance_policies_scope_type_check CHECK ((scope_type = ANY (ARRAY['tenant'::text, 'application'::text, 'client'::text, 'user'::text, 'profile'::text])))
);

ALTER TABLE ONLY them.governance_policies FORCE ROW LEVEL SECURITY;


ALTER TABLE them.governance_policies OWNER TO them_owner;

--
-- Name: hil_approvals; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.hil_approvals (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    application_id uuid NOT NULL,
    run_id uuid NOT NULL,
    node_id text NOT NULL,
    approver_role text DEFAULT 'admin'::text NOT NULL,
    prompt text,
    fallback_action text DEFAULT 'reject'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    decided_at timestamp with time zone,
    comment text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT hil_approvals_fallback_action_check CHECK ((fallback_action = ANY (ARRAY['reject'::text, 'approve'::text, 'abort'::text]))),
    CONSTRAINT hil_approvals_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'timed_out'::text])))
);


ALTER TABLE them.hil_approvals OWNER TO them;

--
-- Name: llm_provider_keys; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.llm_provider_keys (
    id bigint NOT NULL,
    llm_provider_id integer NOT NULL,
    tenant_id uuid,
    name text NOT NULL,
    api_key_encrypted text NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    last_tested_at timestamp with time zone,
    last_test_ok boolean,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.llm_provider_keys FORCE ROW LEVEL SECURITY;


ALTER TABLE them.llm_provider_keys OWNER TO them_owner;

--
-- Name: llm_provider_keys_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.llm_provider_keys_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.llm_provider_keys_id_seq OWNER TO them_owner;

--
-- Name: llm_provider_keys_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.llm_provider_keys_id_seq OWNED BY them.llm_provider_keys.id;


--
-- Name: llm_providers; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.llm_providers (
    id integer NOT NULL,
    name text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    api_key_encrypted text,
    base_url text,
    default_model text,
    model_pricing jsonb,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid,
    allowed_models jsonb DEFAULT '[]'::jsonb NOT NULL
);

ALTER TABLE ONLY them.llm_providers FORCE ROW LEVEL SECURITY;


ALTER TABLE them.llm_providers OWNER TO them_owner;

--
-- Name: llm_providers_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.llm_providers_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.llm_providers_id_seq OWNER TO them_owner;

--
-- Name: llm_providers_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.llm_providers_id_seq OWNED BY them.llm_providers.id;


--
-- Name: mcp_servers; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.mcp_servers (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    slug text NOT NULL,
    description text,
    transport text DEFAULT 'http'::text NOT NULL,
    url text,
    auth_type text DEFAULT 'none'::text NOT NULL,
    health_status text DEFAULT 'unknown'::text NOT NULL,
    last_checked_at timestamp with time zone,
    last_error text,
    tools_manifest jsonb DEFAULT '[]'::jsonb NOT NULL,
    capabilities jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    probe_credential_encrypted text,
    CONSTRAINT mcp_servers_auth_type_check CHECK ((auth_type = ANY (ARRAY['none'::text, 'bearer'::text, 'header'::text, 'oauth2'::text]))),
    CONSTRAINT mcp_servers_health_status_check CHECK ((health_status = ANY (ARRAY['unknown'::text, 'healthy'::text, 'degraded'::text, 'unreachable'::text]))),
    CONSTRAINT mcp_servers_transport_check CHECK ((transport = ANY (ARRAY['http'::text, 'sse'::text, 'streamable-http'::text])))
);

ALTER TABLE ONLY them.mcp_servers FORCE ROW LEVEL SECURITY;


ALTER TABLE them.mcp_servers OWNER TO them_owner;

--
-- Name: TABLE mcp_servers; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON TABLE them.mcp_servers IS 'Platform-registered MCP servers. health_status/tools_manifest/last_checked_at are written exclusively by them-mcp-service. Admin CRUD writes name/slug/url/auth_type/enabled.';


--
-- Name: COLUMN mcp_servers.probe_credential_encrypted; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.mcp_servers.probe_credential_encrypted IS 'Fernet-encrypted token used by them-mcp-service health probe. NULL = no probe auth.';


--
-- Name: middleware_audit; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.middleware_audit (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    artifact_id uuid NOT NULL,
    application_id uuid NOT NULL,
    session_id uuid,
    run_id uuid,
    processor text NOT NULL,
    outcome text NOT NULL,
    detail jsonb,
    duration_ms integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.middleware_audit FORCE ROW LEVEL SECURITY;


ALTER TABLE them.middleware_audit OWNER TO them_owner;

--
-- Name: middleware_defs; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.middleware_defs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    kind text NOT NULL,
    display_name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    config jsonb DEFAULT '{}'::jsonb NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    namespace text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    scope text DEFAULT 'tenant'::text NOT NULL,
    status text DEFAULT 'published'::text NOT NULL,
    content_hash text DEFAULT ''::text NOT NULL,
    emoji text,
    color text,
    bg_color text,
    edges jsonb,
    input_ports jsonb,
    output_ports jsonb,
    config_fields jsonb,
    CONSTRAINT ck_mw_defs_scope CHECK ((scope = ANY (ARRAY['builtin'::text, 'tenant'::text]))),
    CONSTRAINT ck_mw_defs_status CHECK ((status = ANY (ARRAY['draft'::text, 'published'::text, 'deprecated'::text]))),
    CONSTRAINT middleware_defs_kind_check CHECK ((kind = ANY (ARRAY['guard'::text, 'cache'::text])))
);


ALTER TABLE them.middleware_defs OWNER TO them_owner;

--
-- Name: middleware_jobs; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.middleware_jobs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    artifact_id uuid,
    application_id uuid NOT NULL,
    run_id uuid,
    session_id uuid,
    processors text[] NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 3 NOT NULL,
    claimed_at timestamp with time zone,
    retry_after timestamp with time zone,
    result jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    quarantine_id uuid,
    CONSTRAINT middleware_jobs_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'claimed'::text, 'done'::text, 'failed'::text])))
);

ALTER TABLE ONLY them.middleware_jobs FORCE ROW LEVEL SECURITY;


ALTER TABLE them.middleware_jobs OWNER TO them_owner;

--
-- Name: middleware_wirings; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.middleware_wirings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    agent_id uuid,
    def_id uuid NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    config_override jsonb DEFAULT '{}'::jsonb NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    node_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    component_definition_id uuid,
    component_version integer
);

ALTER TABLE ONLY them.middleware_wirings FORCE ROW LEVEL SECURITY;


ALTER TABLE them.middleware_wirings OWNER TO them_owner;

--
-- Name: orchestrators; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.orchestrators (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    name text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    system_prompt text DEFAULT ''::text NOT NULL,
    allowed_agent_ids uuid[] DEFAULT '{}'::uuid[] NOT NULL,
    llm_provider text,
    llm_model text,
    llm_api_key_encrypted text,
    llm_base_url text,
    max_iterations integer DEFAULT 10 NOT NULL,
    max_parallel_tools integer DEFAULT 3 NOT NULL,
    rate_limit_rpm integer,
    daily_budget_usd numeric(10,4),
    voice_enabled boolean DEFAULT false NOT NULL,
    transcription_provider character varying(32),
    transcription_model character varying(64),
    transcription_api_key_encrypted text,
    tts_enabled boolean DEFAULT false NOT NULL,
    tts_provider text,
    tts_voice text,
    tts_api_key_encrypted text,
    memory_enabled boolean DEFAULT false NOT NULL,
    summarize_every_n_calls integer DEFAULT 3 NOT NULL,
    memory_raw_fallback_n integer DEFAULT 5 NOT NULL,
    summarizer_provider text,
    summarizer_model text,
    summarizer_api_key_encrypted text,
    edges text[] DEFAULT '{websocket}'::text[] NOT NULL,
    history_window integer DEFAULT 20 NOT NULL,
    budget_tokens integer,
    delegatable boolean DEFAULT false NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL
);

ALTER TABLE ONLY them.orchestrators FORCE ROW LEVEL SECURITY;


ALTER TABLE them.orchestrators OWNER TO them_owner;

--
-- Name: quarantine_artifacts; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.quarantine_artifacts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    application_id uuid NOT NULL,
    run_id uuid NOT NULL,
    session_id uuid,
    tenant_id uuid NOT NULL,
    filename text NOT NULL,
    content_type text DEFAULT 'application/octet-stream'::text NOT NULL,
    size bigint NOT NULL,
    storage_key text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone DEFAULT (now() + '01:00:00'::interval) NOT NULL
);

ALTER TABLE ONLY them.quarantine_artifacts FORCE ROW LEVEL SECURITY;


ALTER TABLE them.quarantine_artifacts OWNER TO them_owner;

--
-- Name: run_artifacts; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.run_artifacts (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    run_id uuid NOT NULL,
    application_id uuid,
    session_id uuid,
    filename text NOT NULL,
    content_type text DEFAULT 'application/octet-stream'::text NOT NULL,
    size bigint NOT NULL,
    data bytea,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    scan_status text DEFAULT 'disabled'::text NOT NULL,
    scan_result jsonb,
    scanned_at timestamp with time zone,
    storage_key text,
    CONSTRAINT run_artifacts_scan_status_check CHECK ((scan_status = ANY (ARRAY['disabled'::text, 'pending'::text, 'scanning'::text, 'clean'::text, 'infected'::text, 'flagged'::text, 'error'::text, 'failed'::text])))
);

ALTER TABLE ONLY them.run_artifacts FORCE ROW LEVEL SECURITY;


ALTER TABLE them.run_artifacts OWNER TO them_owner;

--
-- Name: run_steps; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.run_steps (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    run_id uuid NOT NULL,
    iteration integer DEFAULT 0 NOT NULL,
    agent_id uuid,
    agent_slug text,
    input jsonb,
    output text,
    status text DEFAULT 'pending'::text NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    error text,
    latency_ms integer,
    ended_at timestamp with time zone,
    node_id text,
    node_kind text,
    CONSTRAINT run_steps_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'running'::text, 'completed'::text, 'failed'::text, 'timeout'::text])))
);

ALTER TABLE ONLY them.run_steps FORCE ROW LEVEL SECURITY;


ALTER TABLE them.run_steps OWNER TO them_owner;

--
-- Name: run_usage; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.run_usage (
    id bigint NOT NULL,
    run_id uuid,
    user_id integer,
    provider text,
    model text,
    tokens_input integer DEFAULT 0 NOT NULL,
    tokens_output integer DEFAULT 0 NOT NULL,
    cost_usd numeric(12,8) DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    llm_provider_key_id bigint
);

ALTER TABLE ONLY them.run_usage FORCE ROW LEVEL SECURITY;


ALTER TABLE them.run_usage OWNER TO them_owner;

--
-- Name: run_usage_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.run_usage_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.run_usage_id_seq OWNER TO them_owner;

--
-- Name: run_usage_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.run_usage_id_seq OWNED BY them.run_usage.id;


--
-- Name: runs; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.runs (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    orchestrator_id uuid,
    orchestrator_name text,
    user_id integer,
    session_id uuid,
    goal text,
    status text DEFAULT 'running'::text NOT NULL,
    final_output text,
    total_tokens_in integer DEFAULT 0 NOT NULL,
    total_tokens_out integer DEFAULT 0 NOT NULL,
    total_cost_usd numeric(12,8) DEFAULT 0 NOT NULL,
    entry_point_slug text,
    parent_run_id uuid,
    events_transport text DEFAULT 'pubsub'::text NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    completed_at timestamp with time zone,
    error text,
    iterations integer DEFAULT 0 NOT NULL,
    ended_at timestamp with time zone,
    tenant_id uuid DEFAULT '00000000-0000-0000-0000-000000000001'::uuid NOT NULL,
    application_id uuid,
    definition_id uuid,
    external_user_id text,
    CONSTRAINT runs_events_transport_check CHECK ((events_transport = ANY (ARRAY['pubsub'::text, 'streams'::text]))),
    CONSTRAINT runs_status_check CHECK ((status = ANY (ARRAY['running'::text, 'completed'::text, 'failed'::text, 'canceled'::text, 'cancelled'::text, 'stopped'::text])))
);

ALTER TABLE ONLY them.runs FORCE ROW LEVEL SECURITY;


ALTER TABLE them.runs OWNER TO them_owner;

--
-- Name: COLUMN runs.events_transport; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.runs.events_transport IS 'Event delivery transport for this run. pubsub = legacy Redis Pub/Sub (at-most-once). streams = Redis Streams with replay (Phase 11c+).';


--
-- Name: COLUMN runs.external_user_id; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.runs.external_user_id IS 'End-user identity for this run. Set server-side from JWKS JWT sub or X-External-User header (only when token.is_backend=true). Empty for internal-user and service-token runs.';


--
-- Name: schema_migrations; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.schema_migrations (
    version text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL,
    checksum text,
    CONSTRAINT ck_schema_migrations_version CHECK ((version ~ '^\d{3}[a-z]?(_[a-z0-9_]+)?$'::text))
);


ALTER TABLE them.schema_migrations OWNER TO them_owner;

--
-- Name: TABLE schema_migrations; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON TABLE them.schema_migrations IS 'Tracks applied schema migrations. Fresh installs pre-populate this for all versions covered by schema_current.sql. Upgrade scripts insert one row per new migration after applying it, under pg_try_advisory_lock(987654321).';


--
-- Name: storage_deletions; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.storage_deletions (
    id bigint NOT NULL,
    tenant_id uuid,
    bucket text NOT NULL,
    storage_key text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    last_error text,
    CONSTRAINT storage_deletions_bucket_check CHECK ((bucket = 'artifacts'::text))
);

ALTER TABLE ONLY them.storage_deletions FORCE ROW LEVEL SECURITY;


ALTER TABLE them.storage_deletions OWNER TO them_owner;

--
-- Name: storage_deletions_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.storage_deletions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.storage_deletions_id_seq OWNER TO them_owner;

--
-- Name: storage_deletions_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.storage_deletions_id_seq OWNED BY them.storage_deletions.id;


--
-- Name: task_messages; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.task_messages (
    id bigint NOT NULL,
    task_id uuid NOT NULL,
    role text NOT NULL,
    parts jsonb,
    seq integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT task_messages_role_check CHECK ((role = ANY (ARRAY['user'::text, 'agent'::text, 'system'::text])))
);

ALTER TABLE ONLY them.task_messages FORCE ROW LEVEL SECURITY;


ALTER TABLE them.task_messages OWNER TO them_owner;

--
-- Name: task_messages_id_seq; Type: SEQUENCE; Schema: them; Owner: them_owner
--

CREATE SEQUENCE them.task_messages_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE them.task_messages_id_seq OWNER TO them_owner;

--
-- Name: task_messages_id_seq; Type: SEQUENCE OWNED BY; Schema: them; Owner: them_owner
--

ALTER SEQUENCE them.task_messages_id_seq OWNED BY them.task_messages.id;


--
-- Name: tasks; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.tasks (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    run_id uuid,
    parent_task_id uuid,
    orchestrator_id uuid,
    agent_id uuid,
    user_id integer,
    context_id uuid,
    state text DEFAULT 'submitted'::text NOT NULL,
    kind text DEFAULT 'root'::text NOT NULL,
    remote_task_id text,
    push_url text,
    status_message jsonb,
    input_message jsonb,
    budget_tokens integer,
    deadline timestamp with time zone,
    max_depth integer DEFAULT 5 NOT NULL,
    tokens_used integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    error text,
    tenant_id uuid NOT NULL,
    external_user_id text,
    CONSTRAINT tasks_kind_check CHECK ((kind = ANY (ARRAY['root'::text, 'delegated'::text]))),
    CONSTRAINT tasks_state_check CHECK ((state = ANY (ARRAY['submitted'::text, 'working'::text, 'input-required'::text, 'completed'::text, 'failed'::text, 'canceled'::text, 'rejected'::text])))
);

ALTER TABLE ONLY them.tasks FORCE ROW LEVEL SECURITY;


ALTER TABLE them.tasks OWNER TO them_owner;

--
-- Name: COLUMN tasks.external_user_id; Type: COMMENT; Schema: them; Owner: them_owner
--

COMMENT ON COLUMN them.tasks.external_user_id IS 'End-user identity asserted by the tenant backend. Set server-side from JWKS JWT sub or from X-External-User header (only when token.is_backend=true). Never caller-supplied from non-backend tokens.';


--
-- Name: tenant_group_mappings; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.tenant_group_mappings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    group_claim text NOT NULL,
    role text NOT NULL,
    priority integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_group_mappings_role_check CHECK ((role = ANY (ARRAY['admin'::text, 'member'::text, 'viewer'::text])))
);

ALTER TABLE ONLY them.tenant_group_mappings FORCE ROW LEVEL SECURITY;


ALTER TABLE them.tenant_group_mappings OWNER TO them_owner;

--
-- Name: tenant_quotas; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.tenant_quotas (
    tenant_id uuid NOT NULL,
    plan text DEFAULT 'trial'::text NOT NULL,
    max_agents integer,
    max_apps integer,
    max_mcp_servers integer,
    max_concurrent_runs integer,
    max_users integer,
    monthly_llm_tokens bigint,
    monthly_runs integer,
    api_requests_per_minute integer,
    runs_per_minute integer,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_quotas_plan_check CHECK ((plan = ANY (ARRAY['trial'::text, 'starter'::text, 'pro'::text, 'enterprise'::text])))
);


ALTER TABLE them.tenant_quotas OWNER TO them_owner;

--
-- Name: tenant_role_grants; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.tenant_role_grants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    role_id uuid NOT NULL,
    application_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE them.tenant_role_grants OWNER TO them;

--
-- Name: tenant_role_mappings; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.tenant_role_mappings (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    source text NOT NULL,
    field text NOT NULL,
    value text NOT NULL,
    role_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tenant_role_mappings_source_check CHECK ((source = ANY (ARRAY['jwt_claim'::text, 'header'::text])))
);


ALTER TABLE them.tenant_role_mappings OWNER TO them;

--
-- Name: tenant_roles; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.tenant_roles (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


ALTER TABLE them.tenant_roles OWNER TO them;

--
-- Name: tenant_runtime_config; Type: TABLE; Schema: them; Owner: them
--

CREATE TABLE them.tenant_runtime_config (
    tenant_id uuid NOT NULL,
    jwks_uri text NOT NULL,
    issuer text NOT NULL,
    audience text,
    sub_claim text DEFAULT 'sub'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

ALTER TABLE ONLY them.tenant_runtime_config FORCE ROW LEVEL SECURITY;


ALTER TABLE them.tenant_runtime_config OWNER TO them;

--
-- Name: tenant_system_agent_config; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.tenant_system_agent_config (
    tenant_id uuid NOT NULL,
    role text NOT NULL,
    mode text DEFAULT 'custom'::text NOT NULL,
    provider_name text,
    key_id bigint,
    custom_provider text,
    custom_model text,
    custom_api_key_encrypted text,
    custom_base_url text,
    custom_system_prompt text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    general_model text,
    CONSTRAINT tenant_system_agent_config_mode_check CHECK ((mode = ANY (ARRAY['general'::text, 'custom'::text])))
);

ALTER TABLE ONLY them.tenant_system_agent_config FORCE ROW LEVEL SECURITY;


ALTER TABLE them.tenant_system_agent_config OWNER TO them_owner;

--
-- Name: tenants; Type: TABLE; Schema: them; Owner: them_owner
--

CREATE TABLE them.tenants (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    idp_config jsonb,
    email_domain text,
    is_bootstrap boolean DEFAULT false NOT NULL,
    logo_url text,
    CONSTRAINT tenants_slug_check CHECK ((slug ~ '^[a-z0-9_-]{1,64}$'::text))
);


ALTER TABLE them.tenants OWNER TO them_owner;

--
-- Name: auth_audit id; Type: DEFAULT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.auth_audit ALTER COLUMN id SET DEFAULT nextval('auth_service.auth_audit_id_seq'::regclass);


--
-- Name: roles id; Type: DEFAULT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.roles ALTER COLUMN id SET DEFAULT nextval('auth_service.roles_id_seq'::regclass);


--
-- Name: teams id; Type: DEFAULT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.teams ALTER COLUMN id SET DEFAULT nextval('auth_service.teams_id_seq'::regclass);


--
-- Name: user_sessions id; Type: DEFAULT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_sessions ALTER COLUMN id SET DEFAULT nextval('auth_service.user_sessions_id_seq'::regclass);


--
-- Name: users id; Type: DEFAULT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users ALTER COLUMN id SET DEFAULT nextval('auth_service.users_id_seq'::regclass);


--
-- Name: audit_logs id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.audit_logs ALTER COLUMN id SET DEFAULT nextval('them.audit_logs_id_seq'::regclass);


--
-- Name: llm_provider_keys id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_provider_keys ALTER COLUMN id SET DEFAULT nextval('them.llm_provider_keys_id_seq'::regclass);


--
-- Name: llm_providers id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_providers ALTER COLUMN id SET DEFAULT nextval('them.llm_providers_id_seq'::regclass);


--
-- Name: run_usage id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_usage ALTER COLUMN id SET DEFAULT nextval('them.run_usage_id_seq'::regclass);


--
-- Name: storage_deletions id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.storage_deletions ALTER COLUMN id SET DEFAULT nextval('them.storage_deletions_id_seq'::regclass);


--
-- Name: task_messages id; Type: DEFAULT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.task_messages ALTER COLUMN id SET DEFAULT nextval('them.task_messages_id_seq'::regclass);


--
-- Name: auth_audit auth_audit_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.auth_audit
    ADD CONSTRAINT auth_audit_pkey PRIMARY KEY (id);


--
-- Name: blacklisted_tokens blacklisted_tokens_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.blacklisted_tokens
    ADD CONSTRAINT blacklisted_tokens_pkey PRIMARY KEY (token_hash);


--
-- Name: roles roles_name_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.roles
    ADD CONSTRAINT roles_name_key UNIQUE (name);


--
-- Name: roles roles_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.roles
    ADD CONSTRAINT roles_pkey PRIMARY KEY (id);


--
-- Name: team_members team_members_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.team_members
    ADD CONSTRAINT team_members_pkey PRIMARY KEY (team_id, user_id);


--
-- Name: teams teams_name_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.teams
    ADD CONSTRAINT teams_name_key UNIQUE (name);


--
-- Name: teams teams_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.teams
    ADD CONSTRAINT teams_pkey PRIMARY KEY (id);


--
-- Name: tenant_memberships tenant_memberships_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.tenant_memberships
    ADD CONSTRAINT tenant_memberships_pkey PRIMARY KEY (id);


--
-- Name: tenant_memberships tenant_memberships_user_id_tenant_id_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.tenant_memberships
    ADD CONSTRAINT tenant_memberships_user_id_tenant_id_key UNIQUE (user_id, tenant_id);


--
-- Name: user_overrides user_overrides_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_overrides
    ADD CONSTRAINT user_overrides_pkey PRIMARY KEY (user_id);


--
-- Name: user_sessions user_sessions_access_token_hash_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_sessions
    ADD CONSTRAINT user_sessions_access_token_hash_key UNIQUE (access_token_hash);


--
-- Name: user_sessions user_sessions_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_sessions
    ADD CONSTRAINT user_sessions_pkey PRIMARY KEY (id);


--
-- Name: user_sessions user_sessions_refresh_token_hash_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_sessions
    ADD CONSTRAINT user_sessions_refresh_token_hash_key UNIQUE (refresh_token_hash);


--
-- Name: users users_api_key_hash_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users
    ADD CONSTRAINT users_api_key_hash_key UNIQUE (api_key_hash);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: users users_username_key; Type: CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users
    ADD CONSTRAINT users_username_key UNIQUE (username);


--
-- Name: access_tokens access_tokens_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.access_tokens
    ADD CONSTRAINT access_tokens_pkey PRIMARY KEY (id);


--
-- Name: access_tokens access_tokens_token_hash_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.access_tokens
    ADD CONSTRAINT access_tokens_token_hash_key UNIQUE (token_hash);


--
-- Name: agent_definitions agent_definitions_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_definitions
    ADD CONSTRAINT agent_definitions_pkey PRIMARY KEY (id);


--
-- Name: agent_definitions agent_definitions_tenant_id_agent_slug_revision_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_definitions
    ADD CONSTRAINT agent_definitions_tenant_id_agent_slug_revision_key UNIQUE (tenant_id, agent_slug, revision);


--
-- Name: agent_runtime_specs agent_runtime_specs_definition_id_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_runtime_specs
    ADD CONSTRAINT agent_runtime_specs_definition_id_key UNIQUE (definition_id);


--
-- Name: agent_runtime_specs agent_runtime_specs_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_runtime_specs
    ADD CONSTRAINT agent_runtime_specs_pkey PRIMARY KEY (id);


--
-- Name: agents agents_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agents
    ADD CONSTRAINT agents_pkey PRIMARY KEY (id);


--
-- Name: agents agents_tenant_slug_unique; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agents
    ADD CONSTRAINT agents_tenant_slug_unique UNIQUE (tenant_id, slug);


--
-- Name: app_agent_bindings app_agent_bindings_application_id_agent_id_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_agent_bindings
    ADD CONSTRAINT app_agent_bindings_application_id_agent_id_key UNIQUE (application_id, agent_id);


--
-- Name: app_agent_bindings app_agent_bindings_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_agent_bindings
    ADD CONSTRAINT app_agent_bindings_pkey PRIMARY KEY (id);


--
-- Name: app_debug_config app_debug_config_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_debug_config
    ADD CONSTRAINT app_debug_config_pkey PRIMARY KEY (application_id);


--
-- Name: app_flow_http_params app_flow_http_params_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_flow_http_params
    ADD CONSTRAINT app_flow_http_params_pkey PRIMARY KEY (application_id, node_id, param_key);


--
-- Name: app_flow_llm_overrides app_flow_llm_overrides_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_flow_llm_overrides
    ADD CONSTRAINT app_flow_llm_overrides_pkey PRIMARY KEY (application_id, node_id);


--
-- Name: app_mcp_credentials app_mcp_credentials_application_id_mcp_server_id_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_mcp_credentials
    ADD CONSTRAINT app_mcp_credentials_application_id_mcp_server_id_key UNIQUE (application_id, mcp_server_id);


--
-- Name: app_mcp_credentials app_mcp_credentials_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_mcp_credentials
    ADD CONSTRAINT app_mcp_credentials_pkey PRIMARY KEY (id);


--
-- Name: app_orchestrators app_orchestrators_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT app_orchestrators_pkey PRIMARY KEY (id);


--
-- Name: app_temporal_config app_temporal_config_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_temporal_config
    ADD CONSTRAINT app_temporal_config_pkey PRIMARY KEY (application_id);


--
-- Name: appflow_debug_presets appflow_debug_presets_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.appflow_debug_presets
    ADD CONSTRAINT appflow_debug_presets_pkey PRIMARY KEY (id);


--
-- Name: appflow_debug_presets appflow_debug_presets_tenant_id_user_id_application_id_name_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.appflow_debug_presets
    ADD CONSTRAINT appflow_debug_presets_tenant_id_user_id_application_id_name_key UNIQUE (tenant_id, user_id, application_id, name);


--
-- Name: application_definitions application_definitions_application_id_revision_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.application_definitions
    ADD CONSTRAINT application_definitions_application_id_revision_key UNIQUE (application_id, revision);


--
-- Name: application_definitions application_definitions_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.application_definitions
    ADD CONSTRAINT application_definitions_pkey PRIMARY KEY (id);


--
-- Name: applications applications_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.applications
    ADD CONSTRAINT applications_pkey PRIMARY KEY (id);


--
-- Name: artifacts artifacts_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.artifacts
    ADD CONSTRAINT artifacts_pkey PRIMARY KEY (id);


--
-- Name: audit_logs audit_logs_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.audit_logs
    ADD CONSTRAINT audit_logs_pkey PRIMARY KEY (id);


--
-- Name: component_definitions component_definitions_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.component_definitions
    ADD CONSTRAINT component_definitions_pkey PRIMARY KEY (id);


--
-- Name: config config_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.config
    ADD CONSTRAINT config_pkey PRIMARY KEY (config_key);


--
-- Name: entry_points entry_points_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT entry_points_pkey PRIMARY KEY (id);


--
-- Name: gateway_clients gateway_clients_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_pkey PRIMARY KEY (id);


--
-- Name: gateway_clients gateway_clients_token_hash_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_token_hash_key UNIQUE (token_hash);


--
-- Name: gateway_profile_steps gateway_profile_steps_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profile_steps
    ADD CONSTRAINT gateway_profile_steps_pkey PRIMARY KEY (id);


--
-- Name: gateway_profile_steps gateway_profile_steps_profile_id_position_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profile_steps
    ADD CONSTRAINT gateway_profile_steps_profile_id_position_key UNIQUE (profile_id, "position");


--
-- Name: gateway_profiles gateway_profiles_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profiles
    ADD CONSTRAINT gateway_profiles_pkey PRIMARY KEY (id);


--
-- Name: gateway_profiles gateway_profiles_tenant_id_name_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profiles
    ADD CONSTRAINT gateway_profiles_tenant_id_name_key UNIQUE (tenant_id, name);


--
-- Name: gateway_request_bodies gateway_request_bodies_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_request_bodies
    ADD CONSTRAINT gateway_request_bodies_pkey PRIMARY KEY (request_id);


--
-- Name: gateway_requests gateway_requests_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_requests
    ADD CONSTRAINT gateway_requests_pkey PRIMARY KEY (id);


--
-- Name: governance_policies governance_policies_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.governance_policies
    ADD CONSTRAINT governance_policies_pkey PRIMARY KEY (id);


--
-- Name: governance_policies governance_policies_tenant_id_scope_type_scope_id_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.governance_policies
    ADD CONSTRAINT governance_policies_tenant_id_scope_type_scope_id_key UNIQUE (tenant_id, scope_type, scope_id);


--
-- Name: hil_approvals hil_approvals_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.hil_approvals
    ADD CONSTRAINT hil_approvals_pkey PRIMARY KEY (id);


--
-- Name: llm_provider_keys llm_provider_keys_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_provider_keys
    ADD CONSTRAINT llm_provider_keys_pkey PRIMARY KEY (id);


--
-- Name: llm_providers llm_providers_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_providers
    ADD CONSTRAINT llm_providers_pkey PRIMARY KEY (id);


--
-- Name: mcp_servers mcp_servers_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.mcp_servers
    ADD CONSTRAINT mcp_servers_pkey PRIMARY KEY (id);


--
-- Name: mcp_servers mcp_servers_tenant_id_slug_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.mcp_servers
    ADD CONSTRAINT mcp_servers_tenant_id_slug_key UNIQUE (tenant_id, slug);


--
-- Name: middleware_audit middleware_audit_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_audit
    ADD CONSTRAINT middleware_audit_pkey PRIMARY KEY (id);


--
-- Name: middleware_defs middleware_defs_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_defs
    ADD CONSTRAINT middleware_defs_pkey PRIMARY KEY (id);


--
-- Name: middleware_defs middleware_defs_slug_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_defs
    ADD CONSTRAINT middleware_defs_slug_key UNIQUE (slug);


--
-- Name: middleware_jobs middleware_jobs_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_jobs
    ADD CONSTRAINT middleware_jobs_pkey PRIMARY KEY (id);


--
-- Name: middleware_wirings middleware_wirings_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT middleware_wirings_pkey PRIMARY KEY (id);


--
-- Name: orchestrators orchestrators_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.orchestrators
    ADD CONSTRAINT orchestrators_pkey PRIMARY KEY (id);


--
-- Name: orchestrators orchestrators_tenant_name_unique; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.orchestrators
    ADD CONSTRAINT orchestrators_tenant_name_unique UNIQUE (tenant_id, name);


--
-- Name: schema_migrations pk_schema_migrations; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.schema_migrations
    ADD CONSTRAINT pk_schema_migrations PRIMARY KEY (version);


--
-- Name: quarantine_artifacts quarantine_artifacts_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.quarantine_artifacts
    ADD CONSTRAINT quarantine_artifacts_pkey PRIMARY KEY (id);


--
-- Name: run_artifacts run_artifacts_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_artifacts
    ADD CONSTRAINT run_artifacts_pkey PRIMARY KEY (id);


--
-- Name: run_steps run_steps_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_steps
    ADD CONSTRAINT run_steps_pkey PRIMARY KEY (id);


--
-- Name: run_usage run_usage_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_usage
    ADD CONSTRAINT run_usage_pkey PRIMARY KEY (id);


--
-- Name: runs runs_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT runs_pkey PRIMARY KEY (id);


--
-- Name: storage_deletions storage_deletions_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.storage_deletions
    ADD CONSTRAINT storage_deletions_pkey PRIMARY KEY (id);


--
-- Name: task_messages task_messages_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.task_messages
    ADD CONSTRAINT task_messages_pkey PRIMARY KEY (id);


--
-- Name: tasks tasks_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);


--
-- Name: tenant_group_mappings tenant_group_mappings_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_group_mappings
    ADD CONSTRAINT tenant_group_mappings_pkey PRIMARY KEY (id);


--
-- Name: tenant_group_mappings tenant_group_mappings_tenant_id_group_claim_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_group_mappings
    ADD CONSTRAINT tenant_group_mappings_tenant_id_group_claim_key UNIQUE (tenant_id, group_claim);


--
-- Name: tenant_quotas tenant_quotas_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_quotas
    ADD CONSTRAINT tenant_quotas_pkey PRIMARY KEY (tenant_id);


--
-- Name: tenant_role_grants tenant_role_grants_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_grants
    ADD CONSTRAINT tenant_role_grants_pkey PRIMARY KEY (id);


--
-- Name: tenant_role_grants tenant_role_grants_role_id_application_id_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_grants
    ADD CONSTRAINT tenant_role_grants_role_id_application_id_key UNIQUE (role_id, application_id);


--
-- Name: tenant_role_mappings tenant_role_mappings_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_mappings
    ADD CONSTRAINT tenant_role_mappings_pkey PRIMARY KEY (id);


--
-- Name: tenant_role_mappings tenant_role_mappings_tenant_id_source_field_value_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_mappings
    ADD CONSTRAINT tenant_role_mappings_tenant_id_source_field_value_key UNIQUE (tenant_id, source, field, value);


--
-- Name: tenant_roles tenant_roles_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_roles
    ADD CONSTRAINT tenant_roles_pkey PRIMARY KEY (id);


--
-- Name: tenant_roles tenant_roles_tenant_id_name_key; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_roles
    ADD CONSTRAINT tenant_roles_tenant_id_name_key UNIQUE (tenant_id, name);


--
-- Name: tenant_runtime_config tenant_runtime_config_pkey; Type: CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_runtime_config
    ADD CONSTRAINT tenant_runtime_config_pkey PRIMARY KEY (tenant_id);


--
-- Name: tenant_system_agent_config tenant_system_agent_config_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_system_agent_config
    ADD CONSTRAINT tenant_system_agent_config_pkey PRIMARY KEY (tenant_id, role);


--
-- Name: tenants tenants_pkey; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenants
    ADD CONSTRAINT tenants_pkey PRIMARY KEY (id);


--
-- Name: tenants tenants_slug_key; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenants
    ADD CONSTRAINT tenants_slug_key UNIQUE (slug);


--
-- Name: app_orchestrators uq_app_orch_app_node; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT uq_app_orch_app_node UNIQUE (application_id, node_id);


--
-- Name: app_orchestrators uq_app_orchestrators_app_name; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT uq_app_orchestrators_app_name UNIQUE (application_id, name);


--
-- Name: applications uq_applications_tenant_slug; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.applications
    ADD CONSTRAINT uq_applications_tenant_slug UNIQUE (tenant_id, slug);


--
-- Name: artifacts uq_artifacts_task_artifact_idx; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.artifacts
    ADD CONSTRAINT uq_artifacts_task_artifact_idx UNIQUE (task_id, artifact_id, append_index);


--
-- Name: entry_points uq_entry_points_app_slug; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT uq_entry_points_app_slug UNIQUE (application_id, slug);


--
-- Name: middleware_wirings uq_mw_wiring_app_agent_pos; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT uq_mw_wiring_app_agent_pos UNIQUE (application_id, agent_id, "position");


--
-- Name: task_messages uq_task_messages_task_seq; Type: CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.task_messages
    ADD CONSTRAINT uq_task_messages_task_seq UNIQUE (task_id, seq);


--
-- Name: idx_auth_audit_created_at; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_auth_audit_created_at ON auth_service.auth_audit USING btree (created_at);


--
-- Name: idx_auth_audit_user_id; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_auth_audit_user_id ON auth_service.auth_audit USING btree (user_id);


--
-- Name: idx_blacklisted_tokens_expires_at; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_blacklisted_tokens_expires_at ON auth_service.blacklisted_tokens USING btree (expires_at);


--
-- Name: idx_tenant_memberships_user_id; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_tenant_memberships_user_id ON auth_service.tenant_memberships USING btree (user_id);


--
-- Name: idx_user_sessions_expires_at; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_user_sessions_expires_at ON auth_service.user_sessions USING btree (expires_at);


--
-- Name: idx_user_sessions_user_id; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_user_sessions_user_id ON auth_service.user_sessions USING btree (user_id);


--
-- Name: idx_users_email; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_users_email ON auth_service.users USING btree (email);


--
-- Name: idx_users_idp_sub_issuer; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE UNIQUE INDEX idx_users_idp_sub_issuer ON auth_service.users USING btree (idp_sub, idp_issuer) WHERE ((idp_sub IS NOT NULL) AND (idp_issuer IS NOT NULL));


--
-- Name: idx_users_role_id; Type: INDEX; Schema: auth_service; Owner: them
--

CREATE INDEX idx_users_role_id ON auth_service.users USING btree (role_id);


--
-- Name: agent_definitions_owner; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX agent_definitions_owner ON them.agent_definitions USING btree (owner_id) WHERE (owner_id IS NOT NULL);


--
-- Name: agent_definitions_tenant_slug; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX agent_definitions_tenant_slug ON them.agent_definitions USING btree (tenant_id, agent_slug);


--
-- Name: agent_definitions_tenant_status; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX agent_definitions_tenant_status ON them.agent_definitions USING btree (tenant_id, status);


--
-- Name: agent_runtime_specs_agent; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX agent_runtime_specs_agent ON them.agent_runtime_specs USING btree (agent_id);


--
-- Name: agent_runtime_specs_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX agent_runtime_specs_tenant ON them.agent_runtime_specs USING btree (tenant_id);


--
-- Name: app_agent_bindings_agent; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX app_agent_bindings_agent ON them.app_agent_bindings USING btree (agent_id);


--
-- Name: app_agent_bindings_application; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX app_agent_bindings_application ON them.app_agent_bindings USING btree (application_id);


--
-- Name: appflow_debug_presets_lookup_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX appflow_debug_presets_lookup_idx ON them.appflow_debug_presets USING btree (tenant_id, user_id, application_id);


--
-- Name: component_definitions_builtin_unique; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX component_definitions_builtin_unique ON them.component_definitions USING btree (kind, namespace, name, version) WHERE (tenant_id IS NULL);


--
-- Name: component_definitions_tenant_unique; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX component_definitions_tenant_unique ON them.component_definitions USING btree (kind, namespace, name, version, tenant_id) WHERE (tenant_id IS NOT NULL);


--
-- Name: gateway_clients_hash_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX gateway_clients_hash_idx ON them.gateway_clients USING btree (token_hash);


--
-- Name: gateway_clients_tenant_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX gateway_clients_tenant_idx ON them.gateway_clients USING btree (tenant_id);


--
-- Name: governance_policies_profile_name_uq; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX governance_policies_profile_name_uq ON them.governance_policies USING btree (tenant_id, name) WHERE (scope_type = 'profile'::text);


--
-- Name: gw_req_bodies_retain_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX gw_req_bodies_retain_idx ON them.gateway_request_bodies USING btree (retain_until);


--
-- Name: gw_req_client_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX gw_req_client_idx ON them.gateway_requests USING btree (client_id);


--
-- Name: gw_req_tenant_created_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX gw_req_tenant_created_idx ON them.gateway_requests USING btree (tenant_id, created_at DESC);


--
-- Name: idx_access_tokens_hash; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_access_tokens_hash ON them.access_tokens USING btree (token_hash);


--
-- Name: idx_access_tokens_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_access_tokens_tenant ON them.access_tokens USING btree (tenant_id);


--
-- Name: idx_access_tokens_user; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_access_tokens_user ON them.access_tokens USING btree (user_id);


--
-- Name: idx_agents_created_by; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_agents_created_by ON them.agents USING btree (created_by) WHERE (created_by IS NOT NULL);


--
-- Name: idx_agents_enabled; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_agents_enabled ON them.agents USING btree (enabled);


--
-- Name: idx_agents_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_agents_tenant ON them.agents USING btree (tenant_id);


--
-- Name: idx_agents_tenant_slug; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_agents_tenant_slug ON them.agents USING btree (tenant_id, slug);


--
-- Name: idx_app_orchestrators_application_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_app_orchestrators_application_id ON them.app_orchestrators USING btree (application_id);


--
-- Name: idx_app_orchestrators_name; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_app_orchestrators_name ON them.app_orchestrators USING btree (name);


--
-- Name: idx_applications_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_applications_tenant ON them.applications USING btree (tenant_id);


--
-- Name: idx_artifacts_ctx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_artifacts_ctx ON them.artifacts USING btree (context_id, created_at);


--
-- Name: idx_artifacts_task; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_artifacts_task ON them.artifacts USING btree (task_id);


--
-- Name: idx_audit_created; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_audit_created ON them.audit_logs USING btree (created_at DESC);


--
-- Name: idx_audit_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_audit_tenant ON them.audit_logs USING btree (tenant_id, created_at DESC) WHERE (tenant_id IS NOT NULL);


--
-- Name: idx_cd_kind_scope; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_cd_kind_scope ON them.component_definitions USING btree (kind, scope);


--
-- Name: idx_cd_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_cd_tenant ON them.component_definitions USING btree (tenant_id) WHERE (tenant_id IS NOT NULL);


--
-- Name: idx_entry_points_app_orchestrator_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_entry_points_app_orchestrator_id ON them.entry_points USING btree (app_orchestrator_id);


--
-- Name: idx_entry_points_application_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_entry_points_application_id ON them.entry_points USING btree (application_id);


--
-- Name: idx_entry_points_required_role; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_entry_points_required_role ON them.entry_points USING btree (required_role);


--
-- Name: idx_entry_points_slug; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_entry_points_slug ON them.entry_points USING btree (slug);


--
-- Name: idx_entry_points_tenant_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_entry_points_tenant_id ON them.entry_points USING btree (tenant_id);


--
-- Name: idx_gateway_clients_governance_profile; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_gateway_clients_governance_profile ON them.gateway_clients USING btree (governance_profile_id);


--
-- Name: idx_gateway_requests_run_id; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_gateway_requests_run_id ON them.gateway_requests USING btree (run_id) WHERE (run_id IS NOT NULL);


--
-- Name: idx_gateway_requests_user_id; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_gateway_requests_user_id ON them.gateway_requests USING btree (user_id);


--
-- Name: idx_hil_approvals_run; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_hil_approvals_run ON them.hil_approvals USING btree (run_id);


--
-- Name: idx_hil_approvals_status; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_hil_approvals_status ON them.hil_approvals USING btree (status) WHERE (status = 'pending'::text);


--
-- Name: idx_hil_approvals_tenant_app; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX idx_hil_approvals_tenant_app ON them.hil_approvals USING btree (tenant_id, application_id);


--
-- Name: idx_llm_provider_keys_provider_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_llm_provider_keys_provider_tenant ON them.llm_provider_keys USING btree (llm_provider_id, tenant_id);


--
-- Name: idx_mcp_servers_enabled; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_mcp_servers_enabled ON them.mcp_servers USING btree (enabled) WHERE (enabled = true);


--
-- Name: idx_mcp_servers_tenant_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_mcp_servers_tenant_id ON them.mcp_servers USING btree (tenant_id);


--
-- Name: idx_mw_defs_kind; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_mw_defs_kind ON them.middleware_defs USING btree (kind);


--
-- Name: idx_mw_wirings_app_agent; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_mw_wirings_app_agent ON them.middleware_wirings USING btree (application_id, agent_id);


--
-- Name: idx_orchestrators_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_orchestrators_tenant ON them.orchestrators USING btree (tenant_id);


--
-- Name: idx_quarantine_expires; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_quarantine_expires ON them.quarantine_artifacts USING btree (expires_at) WHERE (storage_key IS NOT NULL);


--
-- Name: idx_run_artifacts_app; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_artifacts_app ON them.run_artifacts USING btree (application_id) WHERE (application_id IS NOT NULL);


--
-- Name: idx_run_artifacts_run; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_artifacts_run ON them.run_artifacts USING btree (run_id);


--
-- Name: idx_run_artifacts_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_artifacts_tenant ON them.run_artifacts USING btree (tenant_id);


--
-- Name: idx_run_steps_agent; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_steps_agent ON them.run_steps USING btree (agent_id);


--
-- Name: idx_run_steps_run; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_steps_run ON them.run_steps USING btree (run_id, iteration);


--
-- Name: idx_run_steps_run_node; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX idx_run_steps_run_node ON them.run_steps USING btree (run_id, node_id) WHERE (node_id IS NOT NULL);


--
-- Name: idx_run_usage_run; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_usage_run ON them.run_usage USING btree (run_id);


--
-- Name: idx_run_usage_user_created; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_run_usage_user_created ON them.run_usage USING btree (user_id, created_at);


--
-- Name: idx_runs_entry_point_slug; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_entry_point_slug ON them.runs USING btree (entry_point_slug);


--
-- Name: idx_runs_external_user; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_external_user ON them.runs USING btree (external_user_id) WHERE (external_user_id IS NOT NULL);


--
-- Name: idx_runs_orchestrator; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_orchestrator ON them.runs USING btree (orchestrator_id, started_at DESC);


--
-- Name: idx_runs_parent_run_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_parent_run_id ON them.runs USING btree (parent_run_id);


--
-- Name: idx_runs_status; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_status ON them.runs USING btree (status);


--
-- Name: idx_runs_tenant; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_tenant ON them.runs USING btree (tenant_id, started_at DESC);


--
-- Name: idx_runs_user; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_user ON them.runs USING btree (user_id) WHERE (user_id IS NOT NULL);


--
-- Name: idx_runs_user_started; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_runs_user_started ON them.runs USING btree (user_id, started_at DESC);


--
-- Name: idx_storage_deletions_pending; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_storage_deletions_pending ON them.storage_deletions USING btree (attempts, id);


--
-- Name: idx_task_messages; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_task_messages ON them.task_messages USING btree (task_id, seq);


--
-- Name: idx_tasks_context; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_context ON them.tasks USING btree (context_id, created_at);


--
-- Name: idx_tasks_external_user; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_external_user ON them.tasks USING btree (external_user_id) WHERE (external_user_id IS NOT NULL);


--
-- Name: idx_tasks_parent; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_parent ON them.tasks USING btree (parent_task_id);


--
-- Name: idx_tasks_remote; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_remote ON them.tasks USING btree (remote_task_id);


--
-- Name: idx_tasks_run; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_run ON them.tasks USING btree (run_id);


--
-- Name: idx_tasks_state; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_state ON them.tasks USING btree (state) WHERE (state = ANY (ARRAY['submitted'::text, 'working'::text, 'input-required'::text]));


--
-- Name: idx_tasks_tenant_context; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_tenant_context ON them.tasks USING btree (tenant_id, context_id);


--
-- Name: idx_tasks_user; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_user ON them.tasks USING btree (user_id) WHERE (user_id IS NOT NULL);


--
-- Name: idx_tasks_user_id; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tasks_user_id ON them.tasks USING btree (user_id);


--
-- Name: idx_tenants_slug; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX idx_tenants_slug ON them.tenants USING btree (slug);


--
-- Name: llm_provider_keys_default_tenant_uq; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX llm_provider_keys_default_tenant_uq ON them.llm_provider_keys USING btree (llm_provider_id, tenant_id) WHERE is_default;


--
-- Name: llm_provider_keys_name_tenant_uq; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX llm_provider_keys_name_tenant_uq ON them.llm_provider_keys USING btree (llm_provider_id, tenant_id, name);


--
-- Name: llm_providers_name_tenant_uq; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX llm_providers_name_tenant_uq ON them.llm_providers USING btree (name, tenant_id);


--
-- Name: llm_providers_tenant_id_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX llm_providers_tenant_id_idx ON them.llm_providers USING btree (tenant_id) WHERE (tenant_id IS NOT NULL);


--
-- Name: middleware_audit_app_time_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX middleware_audit_app_time_idx ON them.middleware_audit USING btree (application_id, created_at DESC);


--
-- Name: middleware_audit_artifact_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX middleware_audit_artifact_idx ON them.middleware_audit USING btree (artifact_id);


--
-- Name: middleware_jobs_claim_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX middleware_jobs_claim_idx ON them.middleware_jobs USING btree (created_at) WHERE ((status = 'pending'::text) AND (attempt_count < max_attempts));


--
-- Name: run_artifacts_scan_pending_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX run_artifacts_scan_pending_idx ON them.run_artifacts USING btree (scan_status, created_at) WHERE (scan_status = ANY (ARRAY['pending'::text, 'scanning'::text]));


--
-- Name: tenant_group_mappings_tenant_id_idx; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE INDEX tenant_group_mappings_tenant_id_idx ON them.tenant_group_mappings USING btree (tenant_id);


--
-- Name: tenant_role_grants_app_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX tenant_role_grants_app_idx ON them.tenant_role_grants USING btree (application_id);


--
-- Name: tenant_role_grants_role_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX tenant_role_grants_role_idx ON them.tenant_role_grants USING btree (role_id);


--
-- Name: tenant_role_mappings_tenant_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX tenant_role_mappings_tenant_idx ON them.tenant_role_mappings USING btree (tenant_id);


--
-- Name: tenant_roles_tenant_idx; Type: INDEX; Schema: them; Owner: them
--

CREATE INDEX tenant_roles_tenant_idx ON them.tenant_roles USING btree (tenant_id);


--
-- Name: tenants_email_domain_uq; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX tenants_email_domain_uq ON them.tenants USING btree (email_domain) WHERE (email_domain IS NOT NULL);


--
-- Name: uq_mw_wiring_app_node_def; Type: INDEX; Schema: them; Owner: them_owner
--

CREATE UNIQUE INDEX uq_mw_wiring_app_node_def ON them.middleware_wirings USING btree (application_id, node_id, def_id) WHERE ((node_id IS NOT NULL) AND (node_id <> ''::text));


--
-- Name: run_artifacts run_artifacts_enqueue_object_deletion; Type: TRIGGER; Schema: them; Owner: them_owner
--

CREATE TRIGGER run_artifacts_enqueue_object_deletion BEFORE DELETE ON them.run_artifacts FOR EACH ROW EXECUTE FUNCTION them.enqueue_artifact_object_deletion();


--
-- Name: auth_audit auth_audit_user_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.auth_audit
    ADD CONSTRAINT auth_audit_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: team_members team_members_team_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.team_members
    ADD CONSTRAINT team_members_team_id_fkey FOREIGN KEY (team_id) REFERENCES auth_service.teams(id) ON DELETE CASCADE;


--
-- Name: team_members team_members_user_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.team_members
    ADD CONSTRAINT team_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE CASCADE;


--
-- Name: tenant_memberships tenant_memberships_user_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.tenant_memberships
    ADD CONSTRAINT tenant_memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE CASCADE;


--
-- Name: user_overrides user_overrides_user_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_overrides
    ADD CONSTRAINT user_overrides_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE CASCADE;


--
-- Name: user_sessions user_sessions_user_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.user_sessions
    ADD CONSTRAINT user_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE CASCADE;


--
-- Name: users users_role_id_fkey; Type: FK CONSTRAINT; Schema: auth_service; Owner: them
--

ALTER TABLE ONLY auth_service.users
    ADD CONSTRAINT users_role_id_fkey FOREIGN KEY (role_id) REFERENCES auth_service.roles(id);


--
-- Name: access_tokens access_tokens_orchestrator_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.access_tokens
    ADD CONSTRAINT access_tokens_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE CASCADE;


--
-- Name: access_tokens access_tokens_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.access_tokens
    ADD CONSTRAINT access_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: agent_definitions agent_definitions_owner_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_definitions
    ADD CONSTRAINT agent_definitions_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: agent_runtime_specs agent_runtime_specs_agent_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_runtime_specs
    ADD CONSTRAINT agent_runtime_specs_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES them.agents(id) ON DELETE CASCADE;


--
-- Name: agent_runtime_specs agent_runtime_specs_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agent_runtime_specs
    ADD CONSTRAINT agent_runtime_specs_definition_id_fkey FOREIGN KEY (definition_id) REFERENCES them.agent_definitions(id) ON DELETE RESTRICT;


--
-- Name: agents agents_created_by_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agents
    ADD CONSTRAINT agents_created_by_fkey FOREIGN KEY (created_by) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: app_agent_bindings app_agent_bindings_agent_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_agent_bindings
    ADD CONSTRAINT app_agent_bindings_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES them.agents(id) ON DELETE RESTRICT;


--
-- Name: app_agent_bindings app_agent_bindings_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_agent_bindings
    ADD CONSTRAINT app_agent_bindings_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_agent_bindings app_agent_bindings_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_agent_bindings
    ADD CONSTRAINT app_agent_bindings_definition_id_fkey FOREIGN KEY (definition_id) REFERENCES them.agent_definitions(id) ON DELETE SET NULL;


--
-- Name: app_debug_config app_debug_config_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_debug_config
    ADD CONSTRAINT app_debug_config_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_flow_http_params app_flow_http_params_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_flow_http_params
    ADD CONSTRAINT app_flow_http_params_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_flow_llm_overrides app_flow_llm_overrides_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_flow_llm_overrides
    ADD CONSTRAINT app_flow_llm_overrides_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_mcp_credentials app_mcp_credentials_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_mcp_credentials
    ADD CONSTRAINT app_mcp_credentials_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_mcp_credentials app_mcp_credentials_mcp_server_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_mcp_credentials
    ADD CONSTRAINT app_mcp_credentials_mcp_server_id_fkey FOREIGN KEY (mcp_server_id) REFERENCES them.mcp_servers(id) ON DELETE CASCADE;


--
-- Name: app_orchestrators app_orchestrators_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT app_orchestrators_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: app_orchestrators app_orchestrators_component_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT app_orchestrators_component_definition_id_fkey FOREIGN KEY (component_definition_id) REFERENCES them.component_definitions(id) ON DELETE SET NULL;


--
-- Name: app_orchestrators app_orchestrators_orchestrator_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT app_orchestrators_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE SET NULL;


--
-- Name: app_orchestrators app_orchestrators_source_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.app_orchestrators
    ADD CONSTRAINT app_orchestrators_source_definition_id_fkey FOREIGN KEY (source_definition_id) REFERENCES them.application_definitions(id) ON DELETE SET NULL;


--
-- Name: app_temporal_config app_temporal_config_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.app_temporal_config
    ADD CONSTRAINT app_temporal_config_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: appflow_debug_presets appflow_debug_presets_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.appflow_debug_presets
    ADD CONSTRAINT appflow_debug_presets_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: appflow_debug_presets appflow_debug_presets_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.appflow_debug_presets
    ADD CONSTRAINT appflow_debug_presets_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: application_definitions application_definitions_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.application_definitions
    ADD CONSTRAINT application_definitions_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: application_definitions application_definitions_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.application_definitions
    ADD CONSTRAINT application_definitions_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: applications applications_active_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.applications
    ADD CONSTRAINT applications_active_definition_id_fkey FOREIGN KEY (active_definition_id) REFERENCES them.application_definitions(id) ON DELETE SET NULL;


--
-- Name: artifacts artifacts_task_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.artifacts
    ADD CONSTRAINT artifacts_task_id_fkey FOREIGN KEY (task_id) REFERENCES them.tasks(id) ON DELETE CASCADE;


--
-- Name: audit_logs audit_logs_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.audit_logs
    ADD CONSTRAINT audit_logs_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: component_definitions component_definitions_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.component_definitions
    ADD CONSTRAINT component_definitions_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: entry_points entry_points_app_orchestrator_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT entry_points_app_orchestrator_id_fkey FOREIGN KEY (app_orchestrator_id) REFERENCES them.app_orchestrators(id) ON DELETE CASCADE;


--
-- Name: entry_points entry_points_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT entry_points_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: entry_points entry_points_source_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT entry_points_source_definition_id_fkey FOREIGN KEY (source_definition_id) REFERENCES them.application_definitions(id) ON DELETE SET NULL;


--
-- Name: entry_points entry_points_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.entry_points
    ADD CONSTRAINT entry_points_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: access_tokens fk_access_tokens_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.access_tokens
    ADD CONSTRAINT fk_access_tokens_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: agents fk_agents_base_def; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agents
    ADD CONSTRAINT fk_agents_base_def FOREIGN KEY (id) REFERENCES them.component_definitions(id);


--
-- Name: agents fk_agents_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.agents
    ADD CONSTRAINT fk_agents_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: applications fk_applications_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.applications
    ADD CONSTRAINT fk_applications_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: middleware_defs fk_mw_defs_base_def; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_defs
    ADD CONSTRAINT fk_mw_defs_base_def FOREIGN KEY (id) REFERENCES them.component_definitions(id);


--
-- Name: orchestrators fk_orchestrators_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.orchestrators
    ADD CONSTRAINT fk_orchestrators_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: run_artifacts fk_run_artifacts_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_artifacts
    ADD CONSTRAINT fk_run_artifacts_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: runs fk_runs_tenant; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT fk_runs_tenant FOREIGN KEY (tenant_id) REFERENCES them.tenants(id);


--
-- Name: gateway_clients gateway_clients_app_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_app_id_fkey FOREIGN KEY (app_id) REFERENCES them.applications(id) ON DELETE SET NULL;


--
-- Name: gateway_clients gateway_clients_governance_profile_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_governance_profile_id_fkey FOREIGN KEY (governance_profile_id) REFERENCES them.governance_policies(id) ON DELETE SET NULL;


--
-- Name: gateway_clients gateway_clients_profile_fk; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_profile_fk FOREIGN KEY (profile_id) REFERENCES them.gateway_profiles(id) ON DELETE SET NULL;


--
-- Name: gateway_clients gateway_clients_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_clients
    ADD CONSTRAINT gateway_clients_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: gateway_profile_steps gateway_profile_steps_def_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profile_steps
    ADD CONSTRAINT gateway_profile_steps_def_id_fkey FOREIGN KEY (def_id) REFERENCES them.middleware_defs(id);


--
-- Name: gateway_profile_steps gateway_profile_steps_profile_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profile_steps
    ADD CONSTRAINT gateway_profile_steps_profile_id_fkey FOREIGN KEY (profile_id) REFERENCES them.gateway_profiles(id) ON DELETE CASCADE;


--
-- Name: gateway_profiles gateway_profiles_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_profiles
    ADD CONSTRAINT gateway_profiles_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: gateway_request_bodies gateway_request_bodies_request_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_request_bodies
    ADD CONSTRAINT gateway_request_bodies_request_id_fkey FOREIGN KEY (request_id) REFERENCES them.gateway_requests(id) ON DELETE CASCADE;


--
-- Name: gateway_request_bodies gateway_request_bodies_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_request_bodies
    ADD CONSTRAINT gateway_request_bodies_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: gateway_requests gateway_requests_client_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_requests
    ADD CONSTRAINT gateway_requests_client_id_fkey FOREIGN KEY (client_id) REFERENCES them.gateway_clients(id) ON DELETE SET NULL;


--
-- Name: gateway_requests gateway_requests_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_requests
    ADD CONSTRAINT gateway_requests_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: gateway_requests gateway_requests_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.gateway_requests
    ADD CONSTRAINT gateway_requests_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: governance_policies governance_policies_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.governance_policies
    ADD CONSTRAINT governance_policies_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: hil_approvals hil_approvals_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.hil_approvals
    ADD CONSTRAINT hil_approvals_run_id_fkey FOREIGN KEY (run_id) REFERENCES them.runs(id) ON DELETE CASCADE;


--
-- Name: llm_provider_keys llm_provider_keys_llm_provider_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_provider_keys
    ADD CONSTRAINT llm_provider_keys_llm_provider_id_fkey FOREIGN KEY (llm_provider_id) REFERENCES them.llm_providers(id) ON DELETE CASCADE;


--
-- Name: llm_provider_keys llm_provider_keys_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_provider_keys
    ADD CONSTRAINT llm_provider_keys_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: llm_providers llm_providers_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.llm_providers
    ADD CONSTRAINT llm_providers_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: middleware_audit middleware_audit_artifact_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_audit
    ADD CONSTRAINT middleware_audit_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES them.run_artifacts(id) ON DELETE CASCADE;


--
-- Name: middleware_jobs middleware_jobs_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_jobs
    ADD CONSTRAINT middleware_jobs_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: middleware_jobs middleware_jobs_artifact_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_jobs
    ADD CONSTRAINT middleware_jobs_artifact_id_fkey FOREIGN KEY (artifact_id) REFERENCES them.run_artifacts(id) ON DELETE CASCADE;


--
-- Name: middleware_jobs middleware_jobs_quarantine_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_jobs
    ADD CONSTRAINT middleware_jobs_quarantine_id_fkey FOREIGN KEY (quarantine_id) REFERENCES them.quarantine_artifacts(id) ON DELETE SET NULL;


--
-- Name: middleware_wirings middleware_wirings_agent_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT middleware_wirings_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES them.agents(id) ON DELETE CASCADE;


--
-- Name: middleware_wirings middleware_wirings_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT middleware_wirings_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: middleware_wirings middleware_wirings_component_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT middleware_wirings_component_definition_id_fkey FOREIGN KEY (component_definition_id) REFERENCES them.component_definitions(id) ON DELETE SET NULL;


--
-- Name: middleware_wirings middleware_wirings_def_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.middleware_wirings
    ADD CONSTRAINT middleware_wirings_def_id_fkey FOREIGN KEY (def_id) REFERENCES them.middleware_defs(id) ON DELETE RESTRICT;


--
-- Name: quarantine_artifacts quarantine_artifacts_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.quarantine_artifacts
    ADD CONSTRAINT quarantine_artifacts_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: run_artifacts run_artifacts_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_artifacts
    ADD CONSTRAINT run_artifacts_run_id_fkey FOREIGN KEY (run_id) REFERENCES them.runs(id) ON DELETE CASCADE;


--
-- Name: run_steps run_steps_agent_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_steps
    ADD CONSTRAINT run_steps_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES them.agents(id) ON DELETE SET NULL;


--
-- Name: run_steps run_steps_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_steps
    ADD CONSTRAINT run_steps_run_id_fkey FOREIGN KEY (run_id) REFERENCES them.runs(id) ON DELETE CASCADE;


--
-- Name: run_usage run_usage_llm_provider_key_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_usage
    ADD CONSTRAINT run_usage_llm_provider_key_id_fkey FOREIGN KEY (llm_provider_key_id) REFERENCES them.llm_provider_keys(id) ON DELETE SET NULL;


--
-- Name: run_usage run_usage_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_usage
    ADD CONSTRAINT run_usage_run_id_fkey FOREIGN KEY (run_id) REFERENCES them.runs(id) ON DELETE CASCADE;


--
-- Name: run_usage run_usage_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.run_usage
    ADD CONSTRAINT run_usage_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: runs runs_definition_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT runs_definition_id_fkey FOREIGN KEY (definition_id) REFERENCES them.application_definitions(id) ON DELETE SET NULL;


--
-- Name: runs runs_orchestrator_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT runs_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE SET NULL;


--
-- Name: runs runs_parent_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT runs_parent_run_id_fkey FOREIGN KEY (parent_run_id) REFERENCES them.runs(id) ON DELETE SET NULL;


--
-- Name: runs runs_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.runs
    ADD CONSTRAINT runs_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: task_messages task_messages_task_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.task_messages
    ADD CONSTRAINT task_messages_task_id_fkey FOREIGN KEY (task_id) REFERENCES them.tasks(id) ON DELETE CASCADE;


--
-- Name: tasks tasks_agent_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES them.agents(id) ON DELETE SET NULL;


--
-- Name: tasks tasks_orchestrator_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_orchestrator_id_fkey FOREIGN KEY (orchestrator_id) REFERENCES them.orchestrators(id) ON DELETE SET NULL;


--
-- Name: tasks tasks_parent_task_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_parent_task_id_fkey FOREIGN KEY (parent_task_id) REFERENCES them.tasks(id) ON DELETE CASCADE;


--
-- Name: tasks tasks_run_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_run_id_fkey FOREIGN KEY (run_id) REFERENCES them.runs(id) ON DELETE SET NULL;


--
-- Name: tasks tasks_user_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tasks
    ADD CONSTRAINT tasks_user_id_fkey FOREIGN KEY (user_id) REFERENCES auth_service.users(id) ON DELETE SET NULL;


--
-- Name: tenant_group_mappings tenant_group_mappings_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_group_mappings
    ADD CONSTRAINT tenant_group_mappings_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_quotas tenant_quotas_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_quotas
    ADD CONSTRAINT tenant_quotas_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_role_grants tenant_role_grants_application_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_grants
    ADD CONSTRAINT tenant_role_grants_application_id_fkey FOREIGN KEY (application_id) REFERENCES them.applications(id) ON DELETE CASCADE;


--
-- Name: tenant_role_grants tenant_role_grants_role_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_grants
    ADD CONSTRAINT tenant_role_grants_role_id_fkey FOREIGN KEY (role_id) REFERENCES them.tenant_roles(id) ON DELETE CASCADE;


--
-- Name: tenant_role_mappings tenant_role_mappings_role_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_mappings
    ADD CONSTRAINT tenant_role_mappings_role_id_fkey FOREIGN KEY (role_id) REFERENCES them.tenant_roles(id) ON DELETE CASCADE;


--
-- Name: tenant_role_mappings tenant_role_mappings_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_role_mappings
    ADD CONSTRAINT tenant_role_mappings_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_roles tenant_roles_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_roles
    ADD CONSTRAINT tenant_roles_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_runtime_config tenant_runtime_config_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them
--

ALTER TABLE ONLY them.tenant_runtime_config
    ADD CONSTRAINT tenant_runtime_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: tenant_system_agent_config tenant_system_agent_config_key_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_system_agent_config
    ADD CONSTRAINT tenant_system_agent_config_key_id_fkey FOREIGN KEY (key_id) REFERENCES them.llm_provider_keys(id) ON DELETE SET NULL;


--
-- Name: tenant_system_agent_config tenant_system_agent_config_tenant_id_fkey; Type: FK CONSTRAINT; Schema: them; Owner: them_owner
--

ALTER TABLE ONLY them.tenant_system_agent_config
    ADD CONSTRAINT tenant_system_agent_config_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES them.tenants(id) ON DELETE CASCADE;


--
-- Name: access_tokens; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.access_tokens ENABLE ROW LEVEL SECURITY;

--
-- Name: access_tokens access_tokens_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY access_tokens_tenant_isolation ON them.access_tokens TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: agent_definitions; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.agent_definitions ENABLE ROW LEVEL SECURITY;

--
-- Name: agent_definitions agent_definitions_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY agent_definitions_tenant_isolation ON them.agent_definitions TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: agent_runtime_specs; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.agent_runtime_specs ENABLE ROW LEVEL SECURITY;

--
-- Name: agent_runtime_specs agent_runtime_specs_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY agent_runtime_specs_tenant_isolation ON them.agent_runtime_specs TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: agents; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.agents ENABLE ROW LEVEL SECURITY;

--
-- Name: agents agents_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY agents_tenant_isolation ON them.agents TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: app_agent_bindings; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.app_agent_bindings ENABLE ROW LEVEL SECURITY;

--
-- Name: app_agent_bindings app_agent_bindings_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY app_agent_bindings_tenant_isolation ON them.app_agent_bindings TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_agent_bindings.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_agent_bindings.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: app_flow_http_params; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.app_flow_http_params ENABLE ROW LEVEL SECURITY;

--
-- Name: app_flow_http_params app_flow_http_params_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY app_flow_http_params_tenant_isolation ON them.app_flow_http_params TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_flow_http_params.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_flow_http_params.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: app_mcp_credentials; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.app_mcp_credentials ENABLE ROW LEVEL SECURITY;

--
-- Name: app_mcp_credentials app_mcp_credentials_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY app_mcp_credentials_tenant_isolation ON them.app_mcp_credentials TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_mcp_credentials.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_mcp_credentials.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: app_orchestrators; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.app_orchestrators ENABLE ROW LEVEL SECURITY;

--
-- Name: app_orchestrators app_orchestrators_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY app_orchestrators_tenant_isolation ON them.app_orchestrators TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_orchestrators.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = app_orchestrators.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: appflow_debug_presets; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.appflow_debug_presets ENABLE ROW LEVEL SECURITY;

--
-- Name: appflow_debug_presets appflow_debug_presets_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY appflow_debug_presets_tenant_isolation ON them.appflow_debug_presets TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: application_definitions; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.application_definitions ENABLE ROW LEVEL SECURITY;

--
-- Name: application_definitions application_definitions_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY application_definitions_tenant_isolation ON them.application_definitions TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: applications; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.applications ENABLE ROW LEVEL SECURITY;

--
-- Name: applications applications_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY applications_tenant_isolation ON them.applications TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: artifacts; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.artifacts ENABLE ROW LEVEL SECURITY;

--
-- Name: artifacts artifacts_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY artifacts_tenant_isolation ON them.artifacts TO them_app USING ((EXISTS ( SELECT 1
   FROM them.tasks t
  WHERE ((t.id = artifacts.task_id) AND (t.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.tasks t
  WHERE ((t.id = artifacts.task_id) AND (t.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: audit_logs; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.audit_logs ENABLE ROW LEVEL SECURITY;

--
-- Name: audit_logs audit_logs_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY audit_logs_tenant_isolation ON them.audit_logs TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: component_definitions; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.component_definitions ENABLE ROW LEVEL SECURITY;

--
-- Name: component_definitions component_definitions_read; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY component_definitions_read ON them.component_definitions FOR SELECT TO them_app USING (((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid) OR (tenant_id IS NULL)));


--
-- Name: component_definitions component_definitions_write; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY component_definitions_write ON them.component_definitions TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: entry_points; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.entry_points ENABLE ROW LEVEL SECURITY;

--
-- Name: entry_points entry_points_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY entry_points_tenant_isolation ON them.entry_points TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: gateway_clients; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.gateway_clients ENABLE ROW LEVEL SECURITY;

--
-- Name: gateway_profiles; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.gateway_profiles ENABLE ROW LEVEL SECURITY;

--
-- Name: gateway_request_bodies; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.gateway_request_bodies ENABLE ROW LEVEL SECURITY;

--
-- Name: gateway_requests; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.gateway_requests ENABLE ROW LEVEL SECURITY;

--
-- Name: gateway_clients gc_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY gc_tenant_isolation ON them.gateway_clients TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: governance_policies; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.governance_policies ENABLE ROW LEVEL SECURITY;

--
-- Name: governance_policies governance_policies_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY governance_policies_tenant_isolation ON them.governance_policies TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: gateway_request_bodies gw_body_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY gw_body_tenant_isolation ON them.gateway_request_bodies TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: gateway_profiles gw_prof_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY gw_prof_tenant_isolation ON them.gateway_profiles TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: gateway_requests gw_req_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY gw_req_tenant_isolation ON them.gateway_requests TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: llm_provider_keys; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.llm_provider_keys ENABLE ROW LEVEL SECURITY;

--
-- Name: llm_provider_keys llm_provider_keys_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY llm_provider_keys_tenant_isolation ON them.llm_provider_keys TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: llm_providers; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.llm_providers ENABLE ROW LEVEL SECURITY;

--
-- Name: llm_providers llm_providers_read; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY llm_providers_read ON them.llm_providers FOR SELECT TO them_app USING (((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid) OR (tenant_id = '00000000-0000-0000-0000-000000000001'::uuid)));


--
-- Name: llm_providers llm_providers_write; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY llm_providers_write ON them.llm_providers TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: mcp_servers; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.mcp_servers ENABLE ROW LEVEL SECURITY;

--
-- Name: mcp_servers mcp_servers_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY mcp_servers_tenant_isolation ON them.mcp_servers TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: middleware_audit; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.middleware_audit ENABLE ROW LEVEL SECURITY;

--
-- Name: middleware_audit middleware_audit_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY middleware_audit_tenant_isolation ON them.middleware_audit TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_audit.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_audit.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: middleware_jobs; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.middleware_jobs ENABLE ROW LEVEL SECURITY;

--
-- Name: middleware_jobs middleware_jobs_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY middleware_jobs_tenant_isolation ON them.middleware_jobs TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_jobs.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_jobs.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: middleware_wirings; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.middleware_wirings ENABLE ROW LEVEL SECURITY;

--
-- Name: middleware_wirings middleware_wirings_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY middleware_wirings_tenant_isolation ON them.middleware_wirings TO them_app USING ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_wirings.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.applications a
  WHERE ((a.id = middleware_wirings.application_id) AND (a.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: orchestrators; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.orchestrators ENABLE ROW LEVEL SECURITY;

--
-- Name: orchestrators orchestrators_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY orchestrators_tenant_isolation ON them.orchestrators TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: quarantine_artifacts; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.quarantine_artifacts ENABLE ROW LEVEL SECURITY;

--
-- Name: quarantine_artifacts quarantine_artifacts_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY quarantine_artifacts_tenant_isolation ON them.quarantine_artifacts TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: run_artifacts; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.run_artifacts ENABLE ROW LEVEL SECURITY;

--
-- Name: run_artifacts run_artifacts_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY run_artifacts_tenant_isolation ON them.run_artifacts TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: run_steps; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.run_steps ENABLE ROW LEVEL SECURITY;

--
-- Name: run_steps run_steps_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY run_steps_tenant_isolation ON them.run_steps TO them_app USING ((EXISTS ( SELECT 1
   FROM them.runs r
  WHERE ((r.id = run_steps.run_id) AND (r.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.runs r
  WHERE ((r.id = run_steps.run_id) AND (r.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: run_usage; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.run_usage ENABLE ROW LEVEL SECURITY;

--
-- Name: run_usage run_usage_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY run_usage_tenant_isolation ON them.run_usage TO them_app USING ((EXISTS ( SELECT 1
   FROM them.runs r
  WHERE ((r.id = run_usage.run_id) AND (r.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.runs r
  WHERE ((r.id = run_usage.run_id) AND (r.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: runs; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.runs ENABLE ROW LEVEL SECURITY;

--
-- Name: runs runs_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY runs_tenant_isolation ON them.runs TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: storage_deletions; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.storage_deletions ENABLE ROW LEVEL SECURITY;

--
-- Name: task_messages; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.task_messages ENABLE ROW LEVEL SECURITY;

--
-- Name: task_messages task_messages_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY task_messages_tenant_isolation ON them.task_messages TO them_app USING ((EXISTS ( SELECT 1
   FROM them.tasks t
  WHERE ((t.id = task_messages.task_id) AND (t.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))))) WITH CHECK ((EXISTS ( SELECT 1
   FROM them.tasks t
  WHERE ((t.id = task_messages.task_id) AND (t.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)))));


--
-- Name: tasks; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.tasks ENABLE ROW LEVEL SECURITY;

--
-- Name: tasks tasks_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY tasks_tenant_isolation ON them.tasks TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: tenant_group_mappings; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.tenant_group_mappings ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_group_mappings tenant_group_mappings_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY tenant_group_mappings_tenant_isolation ON them.tenant_group_mappings TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: tenant_role_grants; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.tenant_role_grants ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_role_grants tenant_role_grants_rls; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY tenant_role_grants_rls ON them.tenant_role_grants TO them_app USING ((role_id IN ( SELECT tenant_roles.id
   FROM them.tenant_roles
  WHERE (tenant_roles.tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid))));


--
-- Name: tenant_role_mappings; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.tenant_role_mappings ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_role_mappings tenant_role_mappings_rls; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY tenant_role_mappings_rls ON them.tenant_role_mappings TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: tenant_roles; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.tenant_roles ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_roles tenant_roles_rls; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY tenant_roles_rls ON them.tenant_roles TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: tenant_runtime_config; Type: ROW SECURITY; Schema: them; Owner: them
--

ALTER TABLE them.tenant_runtime_config ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_system_agent_config; Type: ROW SECURITY; Schema: them; Owner: them_owner
--

ALTER TABLE them.tenant_system_agent_config ENABLE ROW LEVEL SECURITY;

--
-- Name: tenant_system_agent_config tenant_system_agent_config_tenant_isolation; Type: POLICY; Schema: them; Owner: them_owner
--

CREATE POLICY tenant_system_agent_config_tenant_isolation ON them.tenant_system_agent_config TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: tenant_runtime_config trc_tenant_isolation; Type: POLICY; Schema: them; Owner: them
--

CREATE POLICY trc_tenant_isolation ON them.tenant_runtime_config TO them_app USING ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)) WITH CHECK ((tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid));


--
-- Name: SCHEMA auth_service; Type: ACL; Schema: -; Owner: them
--

GRANT USAGE ON SCHEMA auth_service TO them_admin;


--
-- Name: SCHEMA them; Type: ACL; Schema: -; Owner: them
--

GRANT USAGE ON SCHEMA them TO them_app;
GRANT USAGE ON SCHEMA them TO them_admin;
GRANT USAGE ON SCHEMA them TO them_owner;


--
-- Name: TABLE auth_audit; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.auth_audit TO them_admin;


--
-- Name: SEQUENCE auth_audit_id_seq; Type: ACL; Schema: auth_service; Owner: them
--

GRANT SELECT,USAGE ON SEQUENCE auth_service.auth_audit_id_seq TO them_admin;


--
-- Name: TABLE blacklisted_tokens; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.blacklisted_tokens TO them_admin;


--
-- Name: TABLE roles; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.roles TO them_admin;


--
-- Name: SEQUENCE roles_id_seq; Type: ACL; Schema: auth_service; Owner: them
--

GRANT SELECT,USAGE ON SEQUENCE auth_service.roles_id_seq TO them_admin;


--
-- Name: TABLE team_members; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.team_members TO them_admin;


--
-- Name: TABLE teams; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.teams TO them_admin;


--
-- Name: SEQUENCE teams_id_seq; Type: ACL; Schema: auth_service; Owner: them
--

GRANT SELECT,USAGE ON SEQUENCE auth_service.teams_id_seq TO them_admin;


--
-- Name: TABLE tenant_memberships; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.tenant_memberships TO them_admin;


--
-- Name: TABLE user_overrides; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.user_overrides TO them_admin;


--
-- Name: TABLE user_sessions; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.user_sessions TO them_admin;


--
-- Name: SEQUENCE user_sessions_id_seq; Type: ACL; Schema: auth_service; Owner: them
--

GRANT SELECT,USAGE ON SEQUENCE auth_service.user_sessions_id_seq TO them_admin;


--
-- Name: TABLE users; Type: ACL; Schema: auth_service; Owner: them
--

GRANT ALL ON TABLE auth_service.users TO them_admin;


--
-- Name: SEQUENCE users_id_seq; Type: ACL; Schema: auth_service; Owner: them
--

GRANT SELECT,USAGE ON SEQUENCE auth_service.users_id_seq TO them_admin;


--
-- Name: TABLE access_tokens; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.access_tokens TO them_app;
GRANT ALL ON TABLE them.access_tokens TO them_admin;


--
-- Name: TABLE agent_definitions; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.agent_definitions TO them_app;
GRANT ALL ON TABLE them.agent_definitions TO them_admin;


--
-- Name: TABLE agent_runtime_specs; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.agent_runtime_specs TO them_app;
GRANT ALL ON TABLE them.agent_runtime_specs TO them_admin;


--
-- Name: TABLE agents; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.agents TO them_app;
GRANT ALL ON TABLE them.agents TO them_admin;


--
-- Name: TABLE app_agent_bindings; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_agent_bindings TO them_app;
GRANT ALL ON TABLE them.app_agent_bindings TO them_admin;


--
-- Name: TABLE app_debug_config; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_debug_config TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_debug_config TO them_admin;


--
-- Name: TABLE app_flow_http_params; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_flow_http_params TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_flow_http_params TO them_admin;


--
-- Name: TABLE app_flow_llm_overrides; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_flow_llm_overrides TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_flow_llm_overrides TO them_admin;


--
-- Name: TABLE app_mcp_credentials; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_mcp_credentials TO them_app;
GRANT ALL ON TABLE them.app_mcp_credentials TO them_admin;


--
-- Name: TABLE app_orchestrators; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_orchestrators TO them_app;
GRANT ALL ON TABLE them.app_orchestrators TO them_admin;


--
-- Name: TABLE app_temporal_config; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_temporal_config TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.app_temporal_config TO them_admin;


--
-- Name: TABLE appflow_debug_presets; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.appflow_debug_presets TO them_admin;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.appflow_debug_presets TO them_app;


--
-- Name: TABLE application_definitions; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.application_definitions TO them_app;
GRANT ALL ON TABLE them.application_definitions TO them_admin;


--
-- Name: TABLE applications; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.applications TO them_app;
GRANT ALL ON TABLE them.applications TO them_admin;


--
-- Name: TABLE artifacts; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.artifacts TO them_app;
GRANT ALL ON TABLE them.artifacts TO them_admin;


--
-- Name: TABLE audit_logs; Type: ACL; Schema: them; Owner: them_owner
--

GRANT INSERT ON TABLE them.audit_logs TO them_app;
GRANT ALL ON TABLE them.audit_logs TO them_admin;


--
-- Name: SEQUENCE audit_logs_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.audit_logs_id_seq TO them_app;
GRANT SELECT,USAGE ON SEQUENCE them.audit_logs_id_seq TO them_admin;


--
-- Name: TABLE component_definitions; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE ON TABLE them.component_definitions TO them_app;
GRANT ALL ON TABLE them.component_definitions TO them_admin;


--
-- Name: TABLE config; Type: ACL; Schema: them; Owner: them_owner
--

GRANT ALL ON TABLE them.config TO them_admin;


--
-- Name: TABLE entry_points; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.entry_points TO them_app;
GRANT ALL ON TABLE them.entry_points TO them_admin;


--
-- Name: TABLE gateway_clients; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_clients TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_clients TO them_admin;


--
-- Name: TABLE gateway_profile_steps; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_profile_steps TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_profile_steps TO them_admin;


--
-- Name: TABLE gateway_profiles; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_profiles TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.gateway_profiles TO them_admin;


--
-- Name: TABLE gateway_request_bodies; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE ON TABLE them.gateway_request_bodies TO them_app;
GRANT SELECT,INSERT,DELETE ON TABLE them.gateway_request_bodies TO them_admin;


--
-- Name: TABLE gateway_requests; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT ON TABLE them.gateway_requests TO them_app;
GRANT SELECT,INSERT,DELETE ON TABLE them.gateway_requests TO them_admin;


--
-- Name: TABLE governance_policies; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.governance_policies TO them_admin;
GRANT SELECT ON TABLE them.governance_policies TO them_app;


--
-- Name: TABLE hil_approvals; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,UPDATE ON TABLE them.hil_approvals TO them_app;
GRANT SELECT,INSERT,UPDATE ON TABLE them.hil_approvals TO them_admin;


--
-- Name: TABLE llm_provider_keys; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.llm_provider_keys TO them_admin;
GRANT SELECT ON TABLE them.llm_provider_keys TO them_app;


--
-- Name: SEQUENCE llm_provider_keys_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.llm_provider_keys_id_seq TO them_admin;
GRANT SELECT,USAGE ON SEQUENCE them.llm_provider_keys_id_seq TO them_app;


--
-- Name: TABLE llm_providers; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT ON TABLE them.llm_providers TO them_app;
GRANT ALL ON TABLE them.llm_providers TO them_admin;


--
-- Name: SEQUENCE llm_providers_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.llm_providers_id_seq TO them_app;
GRANT SELECT,USAGE ON SEQUENCE them.llm_providers_id_seq TO them_admin;


--
-- Name: TABLE mcp_servers; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.mcp_servers TO them_app;
GRANT ALL ON TABLE them.mcp_servers TO them_admin;


--
-- Name: TABLE middleware_audit; Type: ACL; Schema: them; Owner: them_owner
--

GRANT ALL ON TABLE them.middleware_audit TO them_admin;
GRANT INSERT ON TABLE them.middleware_audit TO them_app;


--
-- Name: TABLE middleware_defs; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT ON TABLE them.middleware_defs TO them_app;
GRANT ALL ON TABLE them.middleware_defs TO them_admin;


--
-- Name: TABLE middleware_jobs; Type: ACL; Schema: them; Owner: them_owner
--

GRANT INSERT ON TABLE them.middleware_jobs TO them_app;
GRANT ALL ON TABLE them.middleware_jobs TO them_admin;


--
-- Name: TABLE middleware_wirings; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.middleware_wirings TO them_app;
GRANT ALL ON TABLE them.middleware_wirings TO them_admin;


--
-- Name: TABLE orchestrators; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.orchestrators TO them_app;
GRANT ALL ON TABLE them.orchestrators TO them_admin;


--
-- Name: TABLE quarantine_artifacts; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT ON TABLE them.quarantine_artifacts TO them_app;
GRANT ALL ON TABLE them.quarantine_artifacts TO them_admin;


--
-- Name: TABLE run_artifacts; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.run_artifacts TO them_app;
GRANT ALL ON TABLE them.run_artifacts TO them_admin;


--
-- Name: TABLE run_steps; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.run_steps TO them_app;
GRANT ALL ON TABLE them.run_steps TO them_admin;


--
-- Name: TABLE run_usage; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.run_usage TO them_app;
GRANT ALL ON TABLE them.run_usage TO them_admin;


--
-- Name: SEQUENCE run_usage_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.run_usage_id_seq TO them_app;
GRANT SELECT,USAGE ON SEQUENCE them.run_usage_id_seq TO them_admin;


--
-- Name: TABLE runs; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.runs TO them_app;
GRANT ALL ON TABLE them.runs TO them_admin;


--
-- Name: TABLE schema_migrations; Type: ACL; Schema: them; Owner: them_owner
--

GRANT ALL ON TABLE them.schema_migrations TO them_admin;


--
-- Name: TABLE storage_deletions; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,DELETE,UPDATE ON TABLE them.storage_deletions TO them_admin;


--
-- Name: SEQUENCE storage_deletions_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.storage_deletions_id_seq TO them_admin;


--
-- Name: TABLE task_messages; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.task_messages TO them_app;
GRANT ALL ON TABLE them.task_messages TO them_admin;


--
-- Name: SEQUENCE task_messages_id_seq; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,USAGE ON SEQUENCE them.task_messages_id_seq TO them_app;
GRANT SELECT,USAGE ON SEQUENCE them.task_messages_id_seq TO them_admin;


--
-- Name: TABLE tasks; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,UPDATE ON TABLE them.tasks TO them_app;
GRANT ALL ON TABLE them.tasks TO them_admin;


--
-- Name: TABLE tenant_group_mappings; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_group_mappings TO them_app;
GRANT ALL ON TABLE them.tenant_group_mappings TO them_admin;


--
-- Name: TABLE tenant_quotas; Type: ACL; Schema: them; Owner: them_owner
--

GRANT ALL ON TABLE them.tenant_quotas TO them_admin;
GRANT SELECT ON TABLE them.tenant_quotas TO them_app;


--
-- Name: TABLE tenant_role_grants; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_role_grants TO them_admin;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_role_grants TO them_app;


--
-- Name: TABLE tenant_role_mappings; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_role_mappings TO them_admin;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_role_mappings TO them_app;


--
-- Name: TABLE tenant_roles; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_roles TO them_admin;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_roles TO them_app;


--
-- Name: TABLE tenant_runtime_config; Type: ACL; Schema: them; Owner: them
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_runtime_config TO them_app;
GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_runtime_config TO them_admin;


--
-- Name: TABLE tenant_system_agent_config; Type: ACL; Schema: them; Owner: them_owner
--

GRANT SELECT,INSERT,DELETE,UPDATE ON TABLE them.tenant_system_agent_config TO them_admin;
GRANT SELECT ON TABLE them.tenant_system_agent_config TO them_app;


--
-- Name: TABLE tenants; Type: ACL; Schema: them; Owner: them_owner
--

GRANT ALL ON TABLE them.tenants TO them_admin;
GRANT SELECT ON TABLE them.tenants TO them_app;


--
-- PostgreSQL database dump complete
--


