--
-- PostgreSQL database dump
--

\restrict 6JSiptbL87NaTH7lD3QBCBhsQAcVOlou5WbDuhB3HuJQi4YbqMCsILBjBPu4aOc

-- Dumped from database version 16.11
-- Dumped by pg_dump version 17.6

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: client_entities; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.client_entities (
    id bigint NOT NULL,
    user_id bigint,
    email text,
    uuid text,
    security text,
    password text,
    flow text,
    limit_ip bigint,
    total_gb numeric,
    expiry_time bigint,
    enable boolean,
    status text DEFAULT 'active'::text,
    tg_id bigint,
    sub_id text,
    comment text,
    reset bigint,
    created_at bigint,
    updated_at bigint,
    up bigint DEFAULT 0,
    down bigint DEFAULT 0,
    all_time bigint DEFAULT 0,
    last_online bigint DEFAULT 0,
    hwid_enabled boolean DEFAULT false,
    max_hwid bigint DEFAULT 1
);


ALTER TABLE public.client_entities OWNER TO xui_user;

--
-- Name: client_entities_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.client_entities_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.client_entities_id_seq OWNER TO xui_user;

--
-- Name: client_entities_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.client_entities_id_seq OWNED BY public.client_entities.id;


--
-- Name: client_hw_ids; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.client_hw_ids (
    id bigint NOT NULL,
    client_id bigint,
    hwid text,
    device_name text,
    device_os text,
    device_model text,
    os_version text,
    first_seen_at bigint,
    last_seen_at bigint,
    first_seen_ip text,
    is_active boolean DEFAULT true,
    ip_address text,
    user_agent text,
    blocked_at bigint,
    block_reason text
);


ALTER TABLE public.client_hw_ids OWNER TO xui_user;

--
-- Name: client_hw_ids_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.client_hw_ids_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.client_hw_ids_id_seq OWNER TO xui_user;

--
-- Name: client_hw_ids_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.client_hw_ids_id_seq OWNED BY public.client_hw_ids.id;


--
-- Name: client_inbound_mappings; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.client_inbound_mappings (
    id bigint NOT NULL,
    client_id bigint,
    inbound_id bigint
);


ALTER TABLE public.client_inbound_mappings OWNER TO xui_user;

--
-- Name: client_inbound_mappings_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.client_inbound_mappings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.client_inbound_mappings_id_seq OWNER TO xui_user;

--
-- Name: client_inbound_mappings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.client_inbound_mappings_id_seq OWNED BY public.client_inbound_mappings.id;


--
-- Name: client_traffics; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.client_traffics (
    id bigint NOT NULL,
    inbound_id bigint,
    enable boolean,
    email text,
    up bigint,
    down bigint,
    all_time bigint,
    expiry_time bigint,
    total bigint,
    reset bigint DEFAULT 0,
    last_online bigint DEFAULT 0
);


ALTER TABLE public.client_traffics OWNER TO xui_user;

--
-- Name: client_traffics_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.client_traffics_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.client_traffics_id_seq OWNER TO xui_user;

--
-- Name: client_traffics_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.client_traffics_id_seq OWNED BY public.client_traffics.id;


--
-- Name: history_of_seeders; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.history_of_seeders (
    id bigint NOT NULL,
    seeder_name text
);


ALTER TABLE public.history_of_seeders OWNER TO xui_user;

--
-- Name: history_of_seeders_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.history_of_seeders_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.history_of_seeders_id_seq OWNER TO xui_user;

--
-- Name: history_of_seeders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.history_of_seeders_id_seq OWNED BY public.history_of_seeders.id;


--
-- Name: host_inbound_mappings; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.host_inbound_mappings (
    id bigint NOT NULL,
    host_id bigint,
    inbound_id bigint
);


ALTER TABLE public.host_inbound_mappings OWNER TO xui_user;

--
-- Name: host_inbound_mappings_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.host_inbound_mappings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.host_inbound_mappings_id_seq OWNER TO xui_user;

--
-- Name: host_inbound_mappings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.host_inbound_mappings_id_seq OWNED BY public.host_inbound_mappings.id;


--
-- Name: hosts; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.hosts (
    id bigint NOT NULL,
    user_id bigint,
    name text,
    address text,
    port bigint,
    protocol text,
    remark text,
    enable boolean,
    created_at bigint,
    updated_at bigint
);


ALTER TABLE public.hosts OWNER TO xui_user;

--
-- Name: hosts_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.hosts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.hosts_id_seq OWNER TO xui_user;

--
-- Name: hosts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.hosts_id_seq OWNED BY public.hosts.id;


--
-- Name: inbound_client_ips; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.inbound_client_ips (
    id bigint NOT NULL,
    client_email text,
    ips text
);


ALTER TABLE public.inbound_client_ips OWNER TO xui_user;

--
-- Name: inbound_client_ips_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.inbound_client_ips_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.inbound_client_ips_id_seq OWNER TO xui_user;

--
-- Name: inbound_client_ips_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.inbound_client_ips_id_seq OWNED BY public.inbound_client_ips.id;


--
-- Name: inbound_node_mappings; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.inbound_node_mappings (
    id bigint NOT NULL,
    inbound_id bigint,
    node_id bigint
);


ALTER TABLE public.inbound_node_mappings OWNER TO xui_user;

--
-- Name: inbound_node_mappings_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.inbound_node_mappings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.inbound_node_mappings_id_seq OWNER TO xui_user;

--
-- Name: inbound_node_mappings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.inbound_node_mappings_id_seq OWNED BY public.inbound_node_mappings.id;


--
-- Name: inbounds; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.inbounds (
    id bigint NOT NULL,
    user_id bigint,
    up bigint,
    down bigint,
    total bigint,
    all_time bigint DEFAULT 0,
    remark text,
    enable boolean,
    expiry_time bigint,
    traffic_reset text DEFAULT 'never'::text,
    last_traffic_reset_time bigint DEFAULT 0,
    listen text,
    port bigint,
    protocol text,
    settings text,
    stream_settings text,
    tag text,
    sniffing text
);


ALTER TABLE public.inbounds OWNER TO xui_user;

--
-- Name: inbounds_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.inbounds_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.inbounds_id_seq OWNER TO xui_user;

--
-- Name: inbounds_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.inbounds_id_seq OWNED BY public.inbounds.id;


--
-- Name: nodes; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.nodes (
    id bigint NOT NULL,
    name text,
    address text,
    api_key text,
    status text DEFAULT 'unknown'::text,
    last_check bigint DEFAULT 0,
    response_time bigint DEFAULT 0,
    use_tls boolean DEFAULT false,
    cert_path text,
    key_path text,
    insecure_tls boolean DEFAULT false,
    created_at bigint,
    updated_at bigint
);


ALTER TABLE public.nodes OWNER TO xui_user;

--
-- Name: nodes_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.nodes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.nodes_id_seq OWNER TO xui_user;

--
-- Name: nodes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.nodes_id_seq OWNED BY public.nodes.id;


--
-- Name: outbound_traffics; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.outbound_traffics (
    id bigint NOT NULL,
    tag text,
    up bigint DEFAULT 0,
    down bigint DEFAULT 0,
    total bigint DEFAULT 0
);


ALTER TABLE public.outbound_traffics OWNER TO xui_user;

--
-- Name: outbound_traffics_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.outbound_traffics_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.outbound_traffics_id_seq OWNER TO xui_user;

--
-- Name: outbound_traffics_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.outbound_traffics_id_seq OWNED BY public.outbound_traffics.id;


--
-- Name: settings; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.settings (
    id bigint NOT NULL,
    key text,
    value text
);


ALTER TABLE public.settings OWNER TO xui_user;

--
-- Name: settings_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.settings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.settings_id_seq OWNER TO xui_user;

--
-- Name: settings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.settings_id_seq OWNED BY public.settings.id;


--
-- Name: users; Type: TABLE; Schema: public; Owner: xui_user
--

CREATE TABLE public.users (
    id bigint NOT NULL,
    username text,
    password text
);


ALTER TABLE public.users OWNER TO xui_user;

--
-- Name: users_id_seq; Type: SEQUENCE; Schema: public; Owner: xui_user
--

CREATE SEQUENCE public.users_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.users_id_seq OWNER TO xui_user;

--
-- Name: users_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: xui_user
--

ALTER SEQUENCE public.users_id_seq OWNED BY public.users.id;


--
-- Name: client_entities id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_entities ALTER COLUMN id SET DEFAULT nextval('public.client_entities_id_seq'::regclass);


--
-- Name: client_hw_ids id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_hw_ids ALTER COLUMN id SET DEFAULT nextval('public.client_hw_ids_id_seq'::regclass);


--
-- Name: client_inbound_mappings id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_inbound_mappings ALTER COLUMN id SET DEFAULT nextval('public.client_inbound_mappings_id_seq'::regclass);


--
-- Name: client_traffics id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_traffics ALTER COLUMN id SET DEFAULT nextval('public.client_traffics_id_seq'::regclass);


--
-- Name: history_of_seeders id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.history_of_seeders ALTER COLUMN id SET DEFAULT nextval('public.history_of_seeders_id_seq'::regclass);


--
-- Name: host_inbound_mappings id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.host_inbound_mappings ALTER COLUMN id SET DEFAULT nextval('public.host_inbound_mappings_id_seq'::regclass);


--
-- Name: hosts id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.hosts ALTER COLUMN id SET DEFAULT nextval('public.hosts_id_seq'::regclass);


--
-- Name: inbound_client_ips id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbound_client_ips ALTER COLUMN id SET DEFAULT nextval('public.inbound_client_ips_id_seq'::regclass);


--
-- Name: inbound_node_mappings id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbound_node_mappings ALTER COLUMN id SET DEFAULT nextval('public.inbound_node_mappings_id_seq'::regclass);


--
-- Name: inbounds id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbounds ALTER COLUMN id SET DEFAULT nextval('public.inbounds_id_seq'::regclass);


--
-- Name: nodes id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.nodes ALTER COLUMN id SET DEFAULT nextval('public.nodes_id_seq'::regclass);


--
-- Name: outbound_traffics id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.outbound_traffics ALTER COLUMN id SET DEFAULT nextval('public.outbound_traffics_id_seq'::regclass);


--
-- Name: settings id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.settings ALTER COLUMN id SET DEFAULT nextval('public.settings_id_seq'::regclass);


--
-- Name: users id; Type: DEFAULT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.users ALTER COLUMN id SET DEFAULT nextval('public.users_id_seq'::regclass);


--
-- Data for Name: client_entities; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.client_entities (id, user_id, email, uuid, security, password, flow, limit_ip, total_gb, expiry_time, enable, status, tg_id, sub_id, comment, reset, created_at, updated_at, up, down, all_time, last_online, hwid_enabled, max_hwid) FROM stdin;
1	1	kpichugin@icloud.com	04b2592e-34ed-471e-a780-7ea59695b14e	auto	PrbdprSXXK	xtls-rprx-vision	0	0.5	0	t	active	0	5p5t0o54ea41jcql		0	1768342250	1768342354	0	0	0	0	t	3
\.


--
-- Data for Name: client_hw_ids; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.client_hw_ids (id, client_id, hwid, device_name, device_os, device_model, os_version, first_seen_at, last_seen_at, first_seen_ip, is_active, ip_address, user_agent, blocked_at, block_reason) FROM stdin;
\.


--
-- Data for Name: client_inbound_mappings; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.client_inbound_mappings (id, client_id, inbound_id) FROM stdin;
2	1	1
\.


--
-- Data for Name: client_traffics; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.client_traffics (id, inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online) FROM stdin;
\.


--
-- Data for Name: history_of_seeders; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.history_of_seeders (id, seeder_name) FROM stdin;
1	UserPasswordHash
\.


--
-- Data for Name: host_inbound_mappings; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.host_inbound_mappings (id, host_id, inbound_id) FROM stdin;
\.


--
-- Data for Name: hosts; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.hosts (id, user_id, name, address, port, protocol, remark, enable, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: inbound_client_ips; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.inbound_client_ips (id, client_email, ips) FROM stdin;
\.


--
-- Data for Name: inbound_node_mappings; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.inbound_node_mappings (id, inbound_id, node_id) FROM stdin;
\.


--
-- Data for Name: inbounds; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.inbounds (id, user_id, up, down, total, all_time, remark, enable, expiry_time, traffic_reset, last_traffic_reset_time, listen, port, protocol, settings, stream_settings, tag, sniffing) FROM stdin;
1	1	0	0	0	0		t	0	never	0		54518	vless	{\n  "clients": [\n    {\n      "created_at": 1768342250000,\n      "email": "kpichugin@icloud.com",\n      "flow": "xtls-rprx-vision",\n      "id": "04b2592e-34ed-471e-a780-7ea59695b14e"\n    }\n  ],\n  "decryption": "none",\n  "encryption": "none",\n  "testseed": [\n    900,\n    500,\n    900,\n    256\n  ]\n}	{\n  "network": "tcp",\n  "security": "reality",\n  "externalProxy": [],\n  "realitySettings": {\n    "show": false,\n    "xver": 0,\n    "target": "aws.amazon.com:443",\n    "serverNames": [\n      "aws.amazon.com",\n      "amazon.com"\n    ],\n    "privateKey": "EOx_0GUMZDiA_r4reCiAWyn40L2tY8B9vsNwqj-O82A",\n    "minClientVer": "",\n    "maxClientVer": "",\n    "maxTimediff": 0,\n    "shortIds": [\n      "837fc8948286",\n      "c3e87ef20edd20d8",\n      "f889efcf6bb5e0",\n      "556dcf823b",\n      "72f5",\n      "724b78b0",\n      "038599",\n      "01"\n    ],\n    "mldsa65Seed": "",\n    "settings": {\n      "publicKey": "94gpKQKbcF6cts52vKoFRYN2tZsoQgkeo5wWMtwTVmY",\n      "fingerprint": "chrome",\n      "serverName": "",\n      "spiderX": "/",\n      "mldsa65Verify": ""\n    }\n  },\n  "tcpSettings": {\n    "acceptProxyProtocol": false,\n    "header": {\n      "type": "none"\n    }\n  }\n}	inbound-54518	{\n  "enabled": true,\n  "destOverride": [\n    "http",\n    "tls",\n    "quic",\n    "fakedns"\n  ],\n  "metadataOnly": false,\n  "routeOnly": false\n}
\.


--
-- Data for Name: nodes; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.nodes (id, name, address, api_key, status, last_check, response_time, use_tls, cert_path, key_path, insecure_tls, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: outbound_traffics; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.outbound_traffics (id, tag, up, down, total) FROM stdin;
\.


--
-- Data for Name: settings; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.settings (id, key, value) FROM stdin;
1	secret	FHj0OKFmhD1Bv0oQC7OLrq84k22PHzde
\.


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: xui_user
--

COPY public.users (id, username, password) FROM stdin;
1	admin	$2a$10$bahU0./wbw7XrV.9ry0j.ukCVwlkdkuHKnL9E7l/CsclvrgsCXiYW
\.


--
-- Name: client_entities_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.client_entities_id_seq', 1, true);


--
-- Name: client_hw_ids_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.client_hw_ids_id_seq', 1, false);


--
-- Name: client_inbound_mappings_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.client_inbound_mappings_id_seq', 2, true);


--
-- Name: client_traffics_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.client_traffics_id_seq', 1, false);


--
-- Name: history_of_seeders_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.history_of_seeders_id_seq', 1, true);


--
-- Name: host_inbound_mappings_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.host_inbound_mappings_id_seq', 1, false);


--
-- Name: hosts_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.hosts_id_seq', 1, false);


--
-- Name: inbound_client_ips_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.inbound_client_ips_id_seq', 1, false);


--
-- Name: inbound_node_mappings_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.inbound_node_mappings_id_seq', 1, false);


--
-- Name: inbounds_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.inbounds_id_seq', 1, true);


--
-- Name: nodes_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.nodes_id_seq', 1, false);


--
-- Name: outbound_traffics_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.outbound_traffics_id_seq', 1, false);


--
-- Name: settings_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.settings_id_seq', 1, true);


--
-- Name: users_id_seq; Type: SEQUENCE SET; Schema: public; Owner: xui_user
--

SELECT pg_catalog.setval('public.users_id_seq', 1, true);


--
-- Name: client_entities client_entities_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_entities
    ADD CONSTRAINT client_entities_pkey PRIMARY KEY (id);


--
-- Name: client_hw_ids client_hw_ids_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_hw_ids
    ADD CONSTRAINT client_hw_ids_pkey PRIMARY KEY (id);


--
-- Name: client_inbound_mappings client_inbound_mappings_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_inbound_mappings
    ADD CONSTRAINT client_inbound_mappings_pkey PRIMARY KEY (id);


--
-- Name: client_traffics client_traffics_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_traffics
    ADD CONSTRAINT client_traffics_pkey PRIMARY KEY (id);


--
-- Name: history_of_seeders history_of_seeders_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.history_of_seeders
    ADD CONSTRAINT history_of_seeders_pkey PRIMARY KEY (id);


--
-- Name: host_inbound_mappings host_inbound_mappings_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.host_inbound_mappings
    ADD CONSTRAINT host_inbound_mappings_pkey PRIMARY KEY (id);


--
-- Name: hosts hosts_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.hosts
    ADD CONSTRAINT hosts_pkey PRIMARY KEY (id);


--
-- Name: inbound_client_ips inbound_client_ips_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbound_client_ips
    ADD CONSTRAINT inbound_client_ips_pkey PRIMARY KEY (id);


--
-- Name: inbound_node_mappings inbound_node_mappings_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbound_node_mappings
    ADD CONSTRAINT inbound_node_mappings_pkey PRIMARY KEY (id);


--
-- Name: inbounds inbounds_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbounds
    ADD CONSTRAINT inbounds_pkey PRIMARY KEY (id);


--
-- Name: nodes nodes_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.nodes
    ADD CONSTRAINT nodes_pkey PRIMARY KEY (id);


--
-- Name: outbound_traffics outbound_traffics_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.outbound_traffics
    ADD CONSTRAINT outbound_traffics_pkey PRIMARY KEY (id);


--
-- Name: settings settings_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (id);


--
-- Name: client_traffics uni_client_traffics_email; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_traffics
    ADD CONSTRAINT uni_client_traffics_email UNIQUE (email);


--
-- Name: inbound_client_ips uni_inbound_client_ips_client_email; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbound_client_ips
    ADD CONSTRAINT uni_inbound_client_ips_client_email UNIQUE (client_email);


--
-- Name: inbounds uni_inbounds_tag; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.inbounds
    ADD CONSTRAINT uni_inbounds_tag UNIQUE (tag);


--
-- Name: outbound_traffics uni_outbound_traffics_tag; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.outbound_traffics
    ADD CONSTRAINT uni_outbound_traffics_tag UNIQUE (tag);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: idx_client_entities_sub_id; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE INDEX idx_client_entities_sub_id ON public.client_entities USING btree (sub_id);


--
-- Name: idx_client_entities_user_id; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE INDEX idx_client_entities_user_id ON public.client_entities USING btree (user_id);


--
-- Name: idx_client_hwid; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE INDEX idx_client_hwid ON public.client_hw_ids USING btree (client_id, hwid);


--
-- Name: idx_client_inbound; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE UNIQUE INDEX idx_client_inbound ON public.client_inbound_mappings USING btree (client_id, inbound_id);


--
-- Name: idx_enable_traffic_reset; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE INDEX idx_enable_traffic_reset ON public.inbounds USING btree (enable, traffic_reset);


--
-- Name: idx_host_inbound; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE UNIQUE INDEX idx_host_inbound ON public.host_inbound_mappings USING btree (host_id, inbound_id);


--
-- Name: idx_hosts_user_id; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE INDEX idx_hosts_user_id ON public.hosts USING btree (user_id);


--
-- Name: idx_inbound_node; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE UNIQUE INDEX idx_inbound_node ON public.inbound_node_mappings USING btree (inbound_id, node_id);


--
-- Name: idx_user_email; Type: INDEX; Schema: public; Owner: xui_user
--

CREATE UNIQUE INDEX idx_user_email ON public.client_entities USING btree (email);


--
-- Name: client_traffics fk_inbounds_client_stats; Type: FK CONSTRAINT; Schema: public; Owner: xui_user
--

ALTER TABLE ONLY public.client_traffics
    ADD CONSTRAINT fk_inbounds_client_stats FOREIGN KEY (inbound_id) REFERENCES public.inbounds(id);


--
-- PostgreSQL database dump complete
--

\unrestrict 6JSiptbL87NaTH7lD3QBCBhsQAcVOlou5WbDuhB3HuJQi4YbqMCsILBjBPu4aOc

