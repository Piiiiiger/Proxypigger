export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD';
export type ParamLocation =
  | 'path'
  | 'query'
  | 'header'
  | 'body'
  | 'body (form)'
  | 'body (json)'
  | 'body (multipart)';
export type ParamType =
  | 'string'
  | 'integer'
  | 'integer[]'
  | 'string[]'
  | 'number'
  | 'boolean'
  | 'object'
  | 'object[]'
  | 'array'
  | 'file';

export interface EndpointParam {
  name: string;
  in: ParamLocation;
  type: ParamType;
  desc?: string;
  optional?: boolean;
  defaultValue?: string | number | boolean;
  minLength?: number;
  pattern?: string;
  enum?: readonly (string | number | boolean)[];
}

export interface Endpoint {
  method: HttpMethod;
  path: string;
  summary: string;
  description?: string;
  deprecated?: boolean;
  params?: EndpointParam[];
  body?: string;
  response?: string;
  errorResponse?: string;
  errorStatus?: number;
  requestSchema?: Record<string, unknown>;
  bodyRequiredOneOf?: string[];
  responseSchema?: string;
  responseSchemaArray?: boolean;
  responseSchemaArrayNullable?: boolean;
  responseObjectSchema?: Record<string, unknown>;
  responses?: Record<string, Record<string, unknown>>;
  security?: readonly Record<string, readonly string[]>[];
}

export interface SubscriptionHeader {
  name: string;
  desc: string;
}

export interface Section {
  id: string;
  title: string;
  description?: string;
  subHeader?: SubscriptionHeader[];
  endpoints: Endpoint[];
}

// /inbounds/update replaces the whole row, so it takes the same payload as /add.
const inboundBody =
  '{\n  "enable": true,\n  "remark": "VLESS-443",\n  "listen": "",\n  "port": 443,\n  "protocol": "vless",\n  "expiryTime": 0,\n  "total": 0,\n  "settings": {\n    "clients": [{ "id": "...", "email": "user1" }],\n    "decryption": "none",\n    "fallbacks": []\n  },\n  "streamSettings": {\n    "network": "tcp",\n    "security": "reality",\n    "realitySettings": { "show": false, "dest": "..." }\n  },\n  "sniffing": {\n    "enabled": true,\n    "destOverride": ["http", "tls"]\n  }\n}';

const subBalancerBodyParams: EndpointParam[] = [
  {
    name: 'remark',
    in: 'body (form)',
    type: 'string',
    desc: 'Display label, used as the config remarks (required).',
  },
  {
    name: 'strategy',
    in: 'body (form)',
    type: 'string',
    desc: 'Balancer strategy: "leastLoad", "leastPing", "roundRobin" or "random". Default "random".',
    optional: true,
    defaultValue: 'random',
  },
  {
    name: 'inboundIds',
    in: 'body (form)',
    type: 'integer[]',
    desc: 'Repeated form keys selecting the member inbounds (required, at least one).',
  },
  {
    name: 'memberWeights',
    in: 'body (form)',
    type: 'object',
    desc: 'leastLoad only: JSON object mapping inbound id to a static weight > 0, e.g. {"3":0.2}. Lower weight = picked more often; absent ids weigh 1. Rejected for other strategies; entries for unselected inbounds are dropped.',
    optional: true,
  },
  {
    name: 'sortOrder',
    in: 'body (form)',
    type: 'integer',
    desc: '1-based position in the subscription list. Default 1.',
    optional: true,
    defaultValue: 1,
  },
  {
    name: 'enabled',
    in: 'body (form)',
    type: 'boolean',
    desc: 'Whether the balancer is emitted. Default true on create; unchanged when omitted on update.',
    optional: true,
  },
];

const subscriptionHeadResponses = {
  '200': { description: 'Subscription is available. Headers match GET; no response body.' },
  '404': { description: 'No enabled client matches the subscription ID.' },
  '500': { description: 'Subscription generation failed.' },
};

const hwidStatusErrorResponses = {
  '404': { description: 'No enabled client matches the subscription ID. Empty body.' },
  '500': { description: 'Database lookup failed. Empty body.' },
};

export const sections: readonly Section[] = [
  {
    id: 'proxy-chains',
    title: 'Proxy chains',
    description: 'Manage target nodes and their relays for Clash/Mihomo subscriptions.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/proxyChains/list',
        summary: 'List configured proxy chains.',
      },
      {
        method: 'POST',
        path: '/panel/api/proxyChains/add',
        summary: 'Create a chain with distinct target and relay inbounds.',
        body: '{"name":"Relay route","targetInboundId":2,"relayInboundId":1,"enabled":true}',
      },
      {
        method: 'POST',
        path: '/panel/api/proxyChains/update/:id',
        summary: 'Update a chain and its optional direct and relay display names.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Chain id.' }],
        body: '{"name":"Relay route","targetInboundId":2,"relayInboundId":1,"enabled":true}',
      },
      {
        method: 'POST',
        path: '/panel/api/proxyChains/del/:id',
        summary: 'Delete a proxy chain.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Chain id.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/proxyChains/check/:id',
        summary: 'Probe a chain using the configured node runtimes.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Chain id.' }],
      },
    ],
  },

  {
    id: 'authentication',
    title: 'Authentication',
    description:
      'Two authentication modes are supported. UI sessions use a cookie set by the login endpoint. Programmatic clients (bots, scripts, remote panels) authenticate with a Bearer token taken from Settings → Security → API Token. Both work for every endpoint under /panel/api/*.',
    endpoints: [
      {
        method: 'POST',
        path: '/portal-admin',
        summary:
          'Exchange the portal administrator login handoff for an admin session. Requires the matching HttpOnly browser cookie, a single-use token less than a minute old, and two-factor authentication still enabled.',
        body: '{\n  "token": "one-time-browser-bound-token"\n}',
        response: '{\n  "success": true,\n  "redirect": "/panel/"\n}',
      },
      {
        method: 'POST',
        path: '/login',
        summary:
          'Authenticate with username + password and receive a session cookie. Required before any cookie-based API call.',
        params: [
          { name: 'username', in: 'body', type: 'string', desc: 'Panel admin username.' },
          { name: 'password', in: 'body', type: 'string', desc: 'Panel admin password.' },
          {
            name: 'twoFactorCode',
            in: 'body',
            type: 'string',
            desc: 'OTP code when 2FA is enabled. Omit otherwise.',
            optional: true,
          },
          {
            name: 'rememberMe',
            in: 'body',
            type: 'boolean',
            desc: 'Keep the session cookie for 30 days, renewed while in use, instead of the panel session length.',
            optional: true,
          },
        ],
        body: '{\n  "username": "admin",\n  "password": "admin",\n  "twoFactorCode": "123456",\n  "rememberMe": true\n}',
        response: '{\n  "success": true,\n  "msg": "Logged in successfully"\n}',
        errorResponse: '{\n  "success": false,\n  "msg": "Wrong username or password"\n}',
      },
      {
        method: 'POST',
        path: '/logout',
        summary: 'Clear the session cookie. Requires the CSRF header for browser sessions.',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'GET',
        path: '/csrf-token',
        summary:
          'Mint a CSRF token for the current session. The SPA replays it in the X-CSRF-Token header on unsafe requests. Bearer-token callers can skip this — the middleware short-circuits CSRF for authenticated API requests.',
        response: '{\n  "success": true,\n  "obj": "csrf-token-string"\n}',
      },
      {
        method: 'POST',
        path: '/getTwoFactorEnable',
        summary:
          'Returns whether 2FA is enabled on the panel — used by the login page to decide whether to show the OTP field.',
        response: '{\n  "success": true,\n  "obj": false\n}',
      },
    ],
  },

  {
    id: 'inbounds',
    title: 'Inbounds',
    description:
      'Manage inbound configurations and their clients. All endpoints live under /panel/api/inbounds and require a logged-in session or Bearer token. Link-generating endpoints honour forwarded headers only when the request comes from a configured trusted proxy.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/inbounds/list',
        summary:
          'List every inbound owned by the authenticated user, including each inbound’s clientStats traffic counters. settings, streamSettings, and sniffing are returned as nested JSON objects (no escaped strings); legacy callers that send them back as JSON-encoded strings are still accepted on write.',
        responseSchema: 'Inbound',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/list/slim',
        summary:
          'Same shape as /list but with settings.clients[] stripped down to {email, enable, comment} and ClientStats not enriched with UUID/SubId. Use this for list pages; fetch /get/:id when you need the full per-client payload (uuid, password, flow, ...).',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "remark": "VLESS-443",\n      "settings": {\n        "clients": [\n          { "email": "alice", "enable": true }\n        ],\n        "decryption": "none"\n      },\n      "clientStats": []\n    }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/options',
        summary:
          'Lightweight picker projection of the authenticated user’s inbounds. Returns id, remark, tag, protocol, port, a server-computed tlsFlowCapable flag (true for VLESS on TCP with tls or reality, or on XHTTP with VLESS encryption / vlessenc enabled), and ssMethod (the Shadowsocks cipher, empty for non-Shadowsocks inbounds — used by the client UI to generate a valid Shadowsocks 2022 PSK). Use this for dropdowns and attach pickers — it skips settings, streamSettings, and clientStats so the payload stays small even on panels with thousands of clients.',
        responseSchema: 'InboundOption',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/allLinks',
        responseObjectSchema: { type: 'array', nullable: true, items: { type: 'string' } },
        summary:
          'Return every protocol URL (vless://, vmess://, trojan://, ss://, hysteria://, mtproto) across all inbounds and all of their clients. Links are rendered through the subscription engine, so the configured remark template (name-only display part) is applied per client — the same output the client info/QR pages use. Protocols without a URL form (socks, http, mixed, wireguard, dokodemo, tunnel) contribute nothing. Used by the panel’s "Export all inbound links" action.',
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?security=reality&...#Germany-alice",\n    "vmess://eyJ2IjoyLC..."\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/get/:id',
        summary: 'Fetch a single inbound by numeric ID.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/freePort/:nodeId',
        summary:
          'Suggest a port for a new inbound on a host: one that no inbound there uses for TCP or UDP on any interface and that the Xray template’s own API and metrics listeners leave free. On the local panel the port must also be one this machine can bind, since other programs may hold ports no inbound records.',
        params: [
          {
            name: 'nodeId',
            in: 'path',
            type: 'number',
            desc: 'Node ID, or 0 for the local panel.',
          },
        ],
        responseSchema: 'FreePortView',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/add',
        summary:
          'Create a new inbound. Send the full inbound payload (protocol, port, settings, streamSettings, sniffing, remark, expiryTime, total, enable). settings, streamSettings, and sniffing may be sent as nested JSON objects (preferred) or as JSON-encoded strings (legacy).',
        body: inboundBody,
        errorResponse: '{\n  "success": false,\n  "msg": "Port 443 is already in use"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/generate',
        summary:
          'Generate a node on the local panel or a connected agent host. The inbound is created disabled, with sharePort set when NAT exposes it on another public port; the chosen plans’ members get it without their limits being re-applied. It is enabled only once the host runs it: the local panel must bind the port and take it live, an agent must accept the pushed config. A node the host refuses is left as a disabled row and the message says why; an earlier failure removes it.',
        body: '{\n  "inbound": {\n    "remark": "HK-Edge-2",\n    "enable": true,\n    "port": 81,\n    "sharePort": 20443,\n    "protocol": "vless",\n    "nodeId": 5,\n    "settings": { "clients": [], "decryption": "none" },\n    "streamSettings": { "network": "tcp", "security": "reality" },\n    "sniffing": {}\n  },\n  "planIds": [1, 2]\n}',
        requestSchema: { $ref: '#/components/schemas/GenerateNodeRequest' },
        errorResponse:
          '{\n  "success": false,\n  "msg": "the node was left disabled: agent on edge-hk refused its config: listen tcp :81: bind: address already in use"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/del/:id',
        summary: 'Delete an inbound by ID. Also removes its associated client stats rows.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/bulkDel',
        summary:
          'Delete many inbounds in one call. Processes the list sequentially; failures are reported per id and the rest still proceed. Restarts xray at most once.',
        body: '{\n  "ids": [1, 2, 3]\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "deleted": 2,\n    "skipped": [\n      { "id": 3, "reason": "..." }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/update/:id',
        summary:
          'Replace an inbound’s configuration. Body shape mirrors /add, but the inbound keeps its stored client list and enable flag: settings.clients and enable in the body are ignored. Manage clients through the /panel/api/clients endpoints and toggle the inbound with /setEnable.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
        body: inboundBody,
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/setEnable/:id',
        summary:
          'Toggle only the enable flag without serialising the whole settings JSON. Recommended for UI switches on large inbounds.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
        body: '{\n  "enable": false\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/subSortIndex',
        summary:
          'Set only the subscription sort order. Reads the stored inbound, so a reorder cannot carry a stale client list over a concurrent edit.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
        body: '{\n  "subSortIndex": 2\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/resetTraffic',
        summary:
          'Zero out upload + download counters for a single inbound. Does not touch per-client counters.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/delAllClients',
        summary:
          'Remove every client attached to a single inbound while keeping the inbound itself. Collects emails from settings.clients[] and feeds them into the optimized bulk-delete path (runtime user removal + traffic-row cleanup + SyncInbound). Destructive and cannot be undone.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Inbound ID.' }],
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 12\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/resetAllTraffics',
        summary:
          'Reset upload + download counters on every inbound. Destructive — accounting history is lost.',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/import',
        summary:
          'Bulk-import an inbound from a JSON blob (e.g. one exported via the UI). The body uses form encoding with a single "data" field.',
        params: [
          {
            name: 'data',
            in: 'body (form)',
            type: 'string',
            desc: 'JSON-encoded inbound payload.',
          },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/pushClientTraffics',
        summary:
          "Receive a master panel's aggregated per-client usage, keyed by the master's GUID. Stored in a side table used only for the UI display overlay and local quota enforcement — never folded into the local counters that masters poll, so delta accounting stays intact. Called panel-to-panel by the node traffic sync job.",
        params: [
          {
            name: 'masterGuid',
            in: 'body (json)',
            type: 'string',
            desc: 'Stable GUID of the pushing master panel.',
          },
          {
            name: 'traffics',
            in: 'body (json)',
            type: 'object[]',
            desc: 'Client traffic rows; only email/up/down are read.',
          },
        ],
        body: '{\n  "masterGuid": "9f6c2d-…",\n  "traffics": [\n    { "email": "alice", "up": 1048576, "down": 2097152 }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/inbounds/:id/fallbacks',
        summary:
          "List the fallback rules attached to a master VLESS/Trojan TCP-TLS inbound. Each rule links one child inbound (the dest) to optional SNI/ALPN/path/dest/xver match criteria. When dest is empty the child inbound's listen+port is used.",
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Master inbound ID.' }],
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "masterId": 10,\n      "childId": 11,\n      "name": "",\n      "alpn": "",\n      "path": "/vlws",\n      "dest": "",\n      "xver": 2,\n      "sortOrder": 0\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/inbounds/:id/fallbacks',
        summary:
          'Replace the entire fallback list for a master inbound. Body is JSON. Triggers an Xray restart.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Master inbound ID.' },
          {
            name: 'fallbacks',
            in: 'body (json)',
            type: 'object[]',
            desc: 'Array of {childId, name, alpn, path, dest, xver, sortOrder} entries. Leave dest empty to auto-resolve from the child inbound\'s listen+port; set it (e.g. "8443", "127.0.0.1:8443", "/dev/shm/x.sock") to override.',
          },
        ],
        body: '{\n  "fallbacks": [\n    { "childId": 11, "path": "/vlws", "xver": 2 },\n    { "childId": 12, "alpn": "h2", "dest": "8443" }\n  ]\n}',
        response: '{\n  "success": true,\n  "msg": "Inbound updated"\n}',
      },
    ],
  },

  {
    id: 'server',
    title: 'Server',
    description:
      'System status, log retrieval, certificate generators, Xray binary management, and backup/restore. All under /panel/api/server.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/openapi.json',
        summary:
          'Serve this API description as an OpenAPI 3 document — the same file that powers the API Docs page. Requires a session or Bearer token like the rest of /panel/api. Useful for generating clients or importing into API tooling.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/status',
        summary:
          'Real-time machine snapshot: CPU, memory, swap, disk, network IO, load averages, open connections, Xray state. Cached and refreshed every 2 seconds in the background.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "cpu": 12.5,\n    "mem": { "current": 2147483648, "total": 8589934592 },\n    "swap": { "current": 0, "total": 4294967296 },\n    "disk": { "current": 53687091200, "total": 268435456000 },\n    "netIO": { "up": 1073741824, "down": 2147483648 },\n    "xray": { "state": "running", "version": "v25.10.31" },\n    "tcpCount": 42,\n    "load": { "load1": 0.5, "load5": 0.3, "load15": 0.2 }\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/cpuHistory/:bucket',
        summary:
          'Legacy: aggregated CPU history. Use /history/cpu/:bucket instead — same data with a uniform {t, v} shape.',
        params: [
          {
            name: 'bucket',
            in: 'path',
            type: 'number',
            desc: 'Bucket size in seconds. Allowed: 2, 30, 60, 120, 180, 300.',
          },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/history/:metric/:bucket',
        summary:
          'Aggregated time-series for one metric. Returns an array of {t, v} samples covering the last ~6 hours.',
        params: [
          {
            name: 'metric',
            in: 'path',
            type: 'string',
            desc: 'cpu | mem | netUp | netDown | online | load1 | load5 | load15.',
          },
          {
            name: 'bucket',
            in: 'path',
            type: 'number',
            desc: 'Bucket size in seconds. Allowed: 2, 30, 60, 120, 180, 300.',
          },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    { "t": 1700000000, "v": 12.5 },\n    { "t": 1700000002, "v": 13.1 }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayMetricsState',
        summary:
          'Xray runtime metrics state — whether the xray config has a `metrics` block, which expvar keys are flowing, and the current snapshot values for each. Returns an empty state when metrics are not configured.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayMetricsHistory/:metric/:bucket',
        summary:
          'Time-series history for one Xray runtime metric over the last ~6 hours. Same {t, v} shape as /history/:metric/:bucket.',
        params: [
          {
            name: 'metric',
            in: 'path',
            type: 'string',
            desc: 'xrAlloc | xrSys | xrHeapObjects | xrNumGC | xrPauseNs.',
          },
          {
            name: 'bucket',
            in: 'path',
            type: 'number',
            desc: 'Bucket size in seconds. Allowed: 2, 30, 60, 120, 180, 300.',
          },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayObservatory',
        summary:
          'Latest snapshot from the Xray observatory — per-outbound latency, health status, and last-probe time. Only populated when the Xray config has an observatory configured.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/xrayObservatoryHistory/:tag/:bucket',
        summary:
          'Time-series of observatory probe results for one outbound tag. Same {t, v} shape as the other history endpoints.',
        params: [
          {
            name: 'tag',
            in: 'path',
            type: 'string',
            desc: 'Outbound tag from the observatory config.',
          },
          {
            name: 'bucket',
            in: 'path',
            type: 'number',
            desc: 'Bucket size in seconds. Allowed: 2, 30, 60, 120, 180, 300.',
          },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/server/getXrayVersion',
        summary: 'List Xray binary versions available for install on this host.',
        response: '{\n  "success": true,\n  "obj": ["v25.10.31", "v25.9.15", "v25.8.1"]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getPanelUpdateInfo',
        summary: 'Check whether a newer 3x-ui release is available on GitHub.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getUpdateStatus',
        summary:
          'Report the outcome of the most recently launched panel self-update (see POST updatePanel). Compare the returned runId against the one updatePanel returned to tell this run apart from a stale result.',
        responseSchema: 'PanelUpdateStatus',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getConfigJson',
        summary: 'Return the assembled Xray config that\u2019s currently running on this host.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "log": { "loglevel": "warning" },\n    "inbounds": [...],\n    "outbounds": [...],\n    "routing": { "rules": [...] }\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getDb',
        summary:
          'Stream a full database backup as an attachment: the SQLite .db file on SQLite panels, or a pg_dump custom-format archive (.dump) on PostgreSQL panels. Use as a manual backup.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getMigration',
        summary:
          'Stream a cross-engine migration file as an attachment: a .dump (SQL text) on SQLite, or a .db SQLite database built from the live data on PostgreSQL.',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewUUID',
        summary: 'Generate a fresh UUID v4. Convenience helper for client IDs.',
        responseSchema: 'NewUUIDResponse',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getWebCertFiles',
        summary:
          'Return this panel\'s own web TLS certificate and key file paths. The central panel calls it on a node (via the node API token) so "Set Cert from Panel" fills a node-assigned inbound with paths that exist on the node.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "webCertFile": "/root/cert/example.com/fullchain.pem",\n    "webKeyFile": "/root/cert/example.com/privkey.pem"\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/descendants',
        summary:
          'Read-only summaries (guid, parentGuid, name, address, status, versions) of the nodes this panel manages. A parent panel calls it on a node (via the node API token) to surface transitive sub-nodes in a chained topology. Counts are computed by the parent, not returned here.',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "guid": "c3d4-...",\n      "parentGuid": "a1b2-...",\n      "name": "Node3",\n      "address": "10.0.0.3",\n      "status": "online"\n    }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewX25519Cert',
        summary: 'Generate a new X25519 keypair for Reality.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "privateKey": "uN9qLfV3zH8w...",\n    "publicKey": "5v8xPqR2sM7k..."\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewmldsa65',
        summary: 'Generate a new ML-DSA-65 keypair. Returns {seed, verify}.',
        responseSchema: 'MLDSA65Response',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewmlkem768',
        summary: 'Generate a new ML-KEM-768 keypair. Returns {seed, client}.',
        responseSchema: 'MLKEM768Response',
      },
      {
        method: 'GET',
        path: '/panel/api/server/getNewVlessEnc',
        summary:
          'Generate VLESS encryption auth options. Returns an auths array each with id, label, encryption, and decryption fields.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "auths": [\n      { "id": 0, "label": "Auth #0", "encryption": "aes-256-gcm", "decryption": "" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/stopXrayService',
        summary: 'Stop the Xray binary. All proxies go offline immediately.',
        errorResponse: '{\n  "success": false,\n  "msg": "Xray is not running"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/restartXrayService',
        summary:
          'Reload Xray with the current config. Typically required after structural inbound or routing changes.',
        errorResponse: '{\n  "success": false,\n  "msg": "Xray config is invalid: ..."\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/installXray/:version',
        summary:
          'Download and install the specified Xray version. Pass "latest" for the newest release.',
        params: [
          {
            name: 'version',
            in: 'path',
            type: 'string',
            desc: 'Xray tag (e.g. v25.10.31) or "latest".',
          },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/server/updatePanel',
        summary: 'Self-update the panel to the latest version. The server restarts on success.',
        params: [
          {
            name: 'dev',
            in: 'body (form)',
            type: 'boolean',
            desc: "Override this run's channel. Omit to use the panel's configured channel.",
            optional: true,
          },
        ],
        response: '{\n  "success": true,\n  "obj": {\n    "runId": "1735689600123456789"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/setUpdateChannel',
        summary:
          'Toggle the panel update channel between stable and the rolling per-commit dev release. Only effective on dev builds.',
        params: [
          {
            name: 'dev',
            in: 'body (form)',
            type: 'boolean',
            desc: 'true = dev channel, false = stable.',
          },
        ],
        body: 'dev=true',
      },
      {
        method: 'POST',
        path: '/panel/api/server/updateGeofile',
        summary:
          'Refresh the default GeoIP / GeoSite data files. Use the /:fileName variant to update one file.',
      },
      {
        method: 'POST',
        path: '/panel/api/server/updateGeofile/:fileName',
        summary: 'Refresh a single Geo file by filename (e.g. geoip.dat, geosite.dat).',
        params: [
          {
            name: 'fileName',
            in: 'path',
            type: 'string',
            desc: 'Filename of the data file to refresh.',
          },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/server/logs/:count',
        summary: 'Return the last N lines of the panel\u2019s own log.',
        params: [
          { name: 'count', in: 'path', type: 'number', desc: 'Number of trailing log lines.' },
          {
            name: 'level',
            in: 'body (form)',
            type: 'string',
            desc: 'Minimum log level filter.',
            optional: true,
          },
          {
            name: 'syslog',
            in: 'body (form)',
            type: 'boolean',
            desc: 'Read system logs instead of the panel log.',
            optional: true,
          },
        ],
        body: 'level=info&syslog=false',
        responseObjectSchema: { type: 'array', nullable: true, items: { type: 'string' } },
        response:
          '{\n  "success": true,\n  "obj": [\n    "2025/01/01 12:00:00 [INFO] Server started",\n    "2025/01/01 12:00:01 [INFO] Xray is running"\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/xraylogs/:count',
        summary: 'Return the last N lines of the Xray process log.',
        params: [
          { name: 'count', in: 'path', type: 'number', desc: 'Number of trailing log lines.' },
          {
            name: 'filter',
            in: 'body (form)',
            type: 'string',
            desc: 'Keyword filter — only lines containing this string.',
            optional: true,
          },
          {
            name: 'showDirect',
            in: 'body (form)',
            type: 'string',
            desc: '"true" to include direct (freedom) traffic lines.',
            optional: true,
          },
          {
            name: 'showBlocked',
            in: 'body (form)',
            type: 'string',
            desc: '"true" to include blocked (blackhole) traffic lines.',
            optional: true,
          },
          {
            name: 'showProxy',
            in: 'body (form)',
            type: 'string',
            desc: '"true" to include proxy traffic lines.',
            optional: true,
          },
        ],
        body: 'filter=error&showDirect=false&showBlocked=true&showProxy=true',
        responseSchema: 'LogEntry',
        responseSchemaArray: true,
        responseSchemaArrayNullable: true,
      },
      {
        method: 'POST',
        path: '/panel/api/server/amneziawglogs/:count',
        summary:
          'Return live AmneziaWG peer activity (handshake, endpoint, transfer) plus the panel’s own AmneziaWG event lines.',
        params: [
          {
            name: 'count',
            in: 'path',
            type: 'number',
            desc: 'Maximum peer rows and event lines to return.',
          },
          {
            name: 'filter',
            in: 'body (form)',
            type: 'string',
            desc: 'Keyword filter — only rows/lines containing this string.',
            optional: true,
          },
        ],
        body: 'filter=awg1',
        responseSchema: 'AmneziaWGLogs',
      },
      {
        method: 'POST',
        path: '/panel/api/server/importDB',
        summary:
          'Restore the panel DB from an uploaded backup (multipart form, field name "db"). SQLite panels accept a SQLite database (.db) or a SQLite migration dump (.dump); PostgreSQL panels accept a pg_dump archive (.dump), a SQLite database (.db), or a SQLite migration dump. The panel restarts after restore. Destructive.',
        params: [
          {
            name: 'db',
            in: 'body (multipart)',
            type: 'file',
            desc: 'Database backup or migration file to upload.',
          },
          {
            name: 'keepHostSettings',
            in: 'body (multipart)',
            type: 'boolean',
            desc: "Keep this machine's addresses, certificates and node identity. Default true.",
            optional: true,
            defaultValue: true,
          },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/server/getNewEchCert',
        summary:
          'Generate a new ECH (Encrypted Client Hello) keypair and config list for the given SNI.',
        params: [
          {
            name: 'sni',
            in: 'body (form)',
            type: 'string',
            desc: 'Server Name Indication to generate the ECH config for.',
          },
        ],
        body: 'sni=example.com',
        response:
          '{\n  "success": true,\n  "obj": {\n    "echKeySet": "...",\n    "echServerKeys": [...],\n    "echConfigList": "..."\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/getCertHash',
        summary:
          'Compute the hex SHA-256 of a certificate (DER) for pinning (pinnedPeerCertSha256). Provide either a server file path or inline PEM/DER content.',
        bodyRequiredOneOf: ['certFile', 'certContent'],
        params: [
          {
            name: 'certFile',
            in: 'body (form)',
            type: 'string',
            desc: 'Path to a certificate file on the server. Takes precedence over certContent.',
            optional: true,
            pattern: '.*\\S.*',
          },
          {
            name: 'certContent',
            in: 'body (form)',
            type: 'string',
            desc: 'Inline PEM (or DER) certificate content, used when certFile is empty.',
            optional: true,
            pattern: '.*\\S.*',
          },
        ],
        body: 'certFile=/root/cert.crt',
        response: '{\n  "success": true,\n  "obj": [\n    "e8e2d3..."\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/getRemoteCertHash',
        summary:
          'Run `xray tls ping` against a remote server and return its live leaf-certificate SHA-256 hash(es) for pinning (pinnedPeerCertSha256).',
        params: [
          {
            name: 'server',
            in: 'body (form)',
            type: 'string',
            desc: 'Remote server as domain or domain:port (default port 443), e.g. cloudflare-dns.com.',
          },
        ],
        body: 'server=cloudflare-dns.com',
        response: '{\n  "success": true,\n  "obj": [\n    "e8e2d3..."\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/server/scanRealityTarget',
        summary:
          'Run a live TLS 1.3 probe against a candidate REALITY target and return a feasibility verdict (TLS 1.3 + h2 + X25519 + trusted certificate) plus the certificate SAN DNS names. A target on a private/loopback address is reported with privateTarget=true and probed only when allowPrivate is set.',
        params: [
          {
            name: 'target',
            in: 'body (form)',
            type: 'string',
            desc: 'Candidate target as host or host:port (default port 443), e.g. www.cloudflare.com:443.',
          },
          {
            name: 'sni',
            in: 'body (form)',
            type: 'string',
            optional: true,
            desc: 'SNI the handshake sends and the certificate is verified against (the inbound serverNames). Defaults to the target host, which a fronting proxy answers with its default certificate.',
          },
          {
            name: 'xver',
            in: 'body (form)',
            type: 'number',
            optional: true,
            desc: 'PROXY protocol version the target expects (matches the inbound xver). 0 = none.',
          },
          {
            name: 'allowPrivate',
            in: 'body (form)',
            type: 'boolean',
            optional: true,
            desc: 'Probe a private/internal/loopback target (LAN, Docker service name). Default false (SSRF guard blocks it and the response sets privateTarget=true).',
          },
        ],
        body: 'target=www.cloudflare.com:443',
        responseSchema: 'RealityScanResult',
      },
      {
        method: 'POST',
        path: '/panel/api/server/scanRealityTargets',
        summary:
          'Probe/discover REALITY targets and return each verdict ranked by feasibility then latency. Each comma-separated token may be a domain (validated with SNI), a bare IP, or a CIDR range (discovered without SNI by reading the certificate domain). When empty, the realityScanCandidates setting is probed (the built-in seed list if that setting is empty).',
        params: [
          {
            name: 'targets',
            in: 'body (form)',
            type: 'string',
            optional: true,
            desc: 'Optional comma-separated tokens: domain[:port], IP[:port], or CIDR (e.g. 104.16.0.0/24). When omitted, the realityScanCandidates setting is probed (the built-in seed list if that setting is empty).',
          },
        ],
        body: 'targets=104.16.0.0/24,www.apple.com:443',
        responseSchema: 'RealityScanResult',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/server/clientIps',
        summary:
          'Fetch the fully aggregated inbound_client_ips database table. Used by nodes to sync recently active IPs across the cluster.',
        responseSchema: 'InboundClientIps',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/server/clientIps',
        summary:
          'Submit a list of recently active IP timestamps. The panel merges them with the existing database to maintain a unified global IP-limit view.',
        requestSchema: {
          type: 'array',
          items: {
            type: 'object',
            properties: {
              clientEmail: { type: 'string' },
              ips: {
                type: 'array',
                nullable: true,
                items: {
                  type: 'object',
                  properties: { ip: { type: 'string' }, timestamp: { type: 'integer' } },
                  required: ['ip', 'timestamp'],
                },
              },
            },
            required: ['clientEmail', 'ips'],
          },
        },
      },
      {
        method: 'GET',
        path: '/panel/api/server/trafficMultiplier',
        summary:
          "Get the traffic multiplier of this panel's own host: what each byte users move through it counts as toward their quotas (1 counts bytes as they are, 0.1 a tenth, 0 nothing). Agent hosts keep theirs as trafficMultiplier on the node.",
        responseSchema: 'TrafficMultiplierView',
      },
      {
        method: 'POST',
        path: '/panel/api/server/trafficMultiplier',
        summary:
          "Set the traffic multiplier of this panel's own host, a number from 0 to 100; it applies to traffic counted from then on. The host's own counters keep the real bytes. Returns the value as stored.",
        body: '{\n  "multiplier": 0.1\n}',
        requestSchema: { $ref: '#/components/schemas/TrafficMultiplierView' },
        responseSchema: 'TrafficMultiplierView',
      },
    ],
  },

  {
    id: 'clients',
    title: 'Clients',
    description:
      'Manage clients as first-class entities that can be attached to one or more inbounds. A single client row drives the settings.clients entry in every inbound it belongs to. Endpoints live under /panel/api/clients.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/clients/customGroups',
        summary:
          'List custom user groups with member emails. Groups are independent of subscription plans.',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/customGroups/save',
        summary:
          'Create a group with id 0, or rename an existing group. Names must be unique and 1–64 characters.',
        body: '{"id":0,"name":"Friends"}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/customGroups/assign',
        summary:
          'Atomically move users into one group. Use id 0 to leave them ungrouped. Plans and quotas are unchanged.',
        body: '{"id":1,"emails":["alice"]}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/customGroups/delete',
        summary: 'Delete a group and leave its members ungrouped; users are retained.',
        body: '{"id":1}',
      },

      {
        method: 'GET',
        path: '/panel/api/clients/list',
        summary:
          'List every client with its attached inbound IDs and traffic record. The reverse field, if set, is returned as a nested JSON object (legacy JSON-encoded-string form is still accepted on write).',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "email": "alice@example.com",\n      "subId": "abcd1234",\n      "uuid": "...",\n      "totalGB": 53687091200,\n      "expiryTime": 1735689600000,\n      "enable": true,\n      "reverse": null,\n      "inboundIds": [3, 5],\n      "traffic": { "up": 1024, "down": 4096, "enable": true }\n    }\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/list/paged',
        summary:
          'Filter, sort, and paginate clients on the server. Each item is a slim row (no uuid/password/auth/flow/security/reverse/tgId) so the clients page can ship 25-ish rows in a few KB instead of the full table. The response also includes a summary computed across the full DB row set so dashboard counters stay stable as the user paginates or filters: the *Count fields are exact, while the email arrays beside them stop at 200 entries so the payload does not grow with the panel. Page size capped at 200; fetch /get/:email to obtain the full per-client payload for an edit/info modal.',
        params: [
          {
            name: 'page',
            in: 'query',
            type: 'number',
            desc: '1-indexed page number. Defaults to 1.',
            optional: true,
            defaultValue: 1,
          },
          {
            name: 'pageSize',
            in: 'query',
            type: 'number',
            desc: 'Rows per page. Defaults to 25, capped at 200.',
            optional: true,
            defaultValue: 25,
          },
          {
            name: 'search',
            in: 'query',
            type: 'string',
            desc: "Case-insensitive substring match on email, subId, comment, UUID, password, auth, Telegram ID or the name of the client's plan.",
            optional: true,
          },
          {
            name: 'filter',
            in: 'query',
            type: 'string',
            desc: 'CSV status buckets: online, active, deactive, depleted, expiring, exhausted (quota used up) or expired (past its expiry). depleted is exhausted or expired. Values are ORed.',
            optional: true,
          },
          {
            name: 'protocol',
            in: 'query',
            type: 'string',
            desc: 'CSV inbound protocols: vmess, vless, trojan, shadowsocks, wireguard, hysteria, http, mixed, tunnel, tun, mtproto or amneziawg. Values are ORed.',
            optional: true,
          },
          {
            name: 'inbound',
            in: 'query',
            type: 'string',
            desc: 'CSV positive inbound IDs. Values are ORed; invalid or non-positive IDs are ignored.',
            optional: true,
          },
          {
            name: 'sort',
            in: 'query',
            type: 'string',
            desc: 'Sort key. An omitted or unknown value falls back to client ID ascending.',
            optional: true,
            enum: [
              'enable',
              'email',
              'inboundIds',
              'traffic',
              'remaining',
              'expiryTime',
              'createdAt',
              'updatedAt',
              'lastOnline',
            ],
          },
          {
            name: 'order',
            in: 'query',
            type: 'string',
            desc: 'Sort direction. Only descend selects descending order; otherwise ascending.',
            optional: true,
            enum: ['ascend', 'descend'],
          },
          {
            name: 'expiryFrom',
            in: 'query',
            type: 'number',
            desc: 'Inclusive minimum expiry time in Unix milliseconds. Zero or negative means unset.',
            optional: true,
          },
          {
            name: 'expiryTo',
            in: 'query',
            type: 'number',
            desc: 'Inclusive maximum expiry time in Unix milliseconds. Zero or negative means unbounded.',
            optional: true,
          },
          {
            name: 'usageFrom',
            in: 'query',
            type: 'number',
            desc: 'Inclusive minimum combined upload and download usage in bytes. Zero means unset.',
            optional: true,
          },
          {
            name: 'usageTo',
            in: 'query',
            type: 'number',
            desc: 'Inclusive maximum combined upload and download usage in bytes. Zero means unbounded.',
            optional: true,
          },
          {
            name: 'autoRenew',
            in: 'query',
            type: 'string',
            desc: 'on selects clients with an interval or calendar-day reset; off selects clients without either.',
            optional: true,
            enum: ['on', 'off'],
          },
          {
            name: 'hasTgId',
            in: 'query',
            type: 'string',
            desc: 'yes selects clients with a non-zero Telegram ID; no selects clients without one.',
            optional: true,
            enum: ['yes', 'no'],
          },
          {
            name: 'hasComment',
            in: 'query',
            type: 'string',
            desc: 'yes selects clients with a non-blank comment; no selects clients without one.',
            optional: true,
            enum: ['yes', 'no'],
          },
          {
            name: 'plan',
            in: 'query',
            type: 'string',
            desc: 'CSV plan ids; 0 matches clients on no plan. Values are ORed.',
            optional: true,
          },
        ],
        responseSchema: 'ClientPageResponse',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/get/:email',
        summary:
          'Fetch one client by email, including the inbound IDs and external config IDs it is attached to.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": {\n    "client": { "id": 1, "email": "alice@example.com", ... },\n    "inboundIds": [3, 5],\n    "externalLinks": [\n      { "id": 11, "kind": "link", "value": "vless://...", "remark": "DE", "enable": true, "expiryTime": 0 },\n      { "id": 12, "kind": "subscription", "value": "https://provider.example/sub/abc", "remark": "Provider", "enable": false, "expiryTime": 1767225600000, "namePrefix": "[zjh] ", "lastFetchAt": 1767220000000, "lastFetchError": "" }\n    ]\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/get/tgId/:tgId',
        summary:
          'Fetch clients by Telegram user ID. Returns an array since multiple clients can share the same Telegram ID.',
        params: [
          { name: 'tgId', in: 'path', type: 'integer', desc: 'Telegram user ID (numeric).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "client": { "id": 1, "email": "alice@example.com", ... },\n      "inboundIds": [3, 5],\n      "externalLinks": [],\n      "usedTraffic": 1048576\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/add',
        summary:
          'Create a new client and attach it to one or more inbounds in a single call. Body is JSON. Per-protocol secrets are generated server-side when omitted, so callers can send only the universal fields.',
        description:
          'Fields the server fills in when they are omitted — a valid value sent by the caller is never overwritten. Re-adding an email that already exists, with its stored `subId`, reuses the stored `id`, `password`, `auth` and `secret` instead of minting new ones, so the identity stays in sync across its inbounds.\n\n- **VLESS / VMess** — `id`, a fresh UUID\n- **Trojan** — `password`\n- **Shadowsocks** — `password`. On a `2022-blake3-*` inbound a supplied password that does not base64-decode to the key length of the cipher (16 or 32 bytes) is replaced by a generated key and the call still succeeds, so read the client back if you did not let the server pick. Legacy ciphers keep any non-empty password\n- **Hysteria** — `auth`\n- **mtproto** — `secret`, a FakeTLS secret derived from the fronting domain of the inbound, or from `www.cloudflare.com` when it has none\n- **WireGuard** — `privateKey` and `publicKey` when both are blank, or `publicKey` alone when only a `privateKey` was sent, plus `allowedIPs`: one free `/32` taken from the /24 the existing peers of that inbound already sit in, or from `10.0.0.0/24` when it has none\n\nAccepted on the same body but never generated: `preSharedKey` and `keepAlive` (WireGuard), `adTag` (mtproto).\n\nWireGuard is the only one of these that can fail. Allocation widens the search to the containing /16 before giving up with `inbound <id>: wireguard: no free address available in <scope>`, and an `allowedIPs` supplied by the caller is validated instead of allocated: `inbound <id>: wireguard: allowedIPs entry <entry> overlaps <address> used by another client` when its range overlaps an address or prefix a different client of that same inbound holds, or `... used by a client on <inbound>` when the holder sits on another WireGuard or AmneziaWG inbound. Ranges are compared, not strings, so `10.0.0.9/24` collides with `10.0.0.5/32`; a `0.0.0.0/0` or `::/0` default route claims no address. Allocation likewise skips every address inside a prefix another client holds. The same validation runs on POST /panel/api/clients/{email}/attach, where a client that already carries an address brings it along.\n\nAn `inboundIds` entry that names no existing inbound rejects the whole call before anything is written. Past that, the inbounds are applied concurrently and independently: one that fails no longer stops the others, so a `success:false` response can still have created the client on the rest. Every error names the inbound it came from (`inbound 7: <message>`), and several failures are reported together, one per line. `limitHwid` is applied only when every inbound succeeded, so re-run the call after fixing the failure.',
        params: [
          {
            name: 'client',
            in: 'body (json)',
            type: 'object',
            desc: 'Client fields: email, subId, id (uuid), password, auth, flow, totalGB, expiryTime, limitIp, limitHwid, tgId (numeric Telegram user ID, 0 = none), comment, enable. Protocol-specific: secret and adTag (mtproto), privateKey, publicKey, preSharedKey, allowedIPs and keepAlive (WireGuard).',
          },
          {
            name: 'inboundIds',
            in: 'body (json)',
            type: 'integer[]',
            desc: 'Inbound IDs to attach the client to. At least one required.',
          },
        ],
        body: '{\n  "client": {\n    "email": "alice@example.com",\n    "totalGB": 53687091200,\n    "expiryTime": 1735689600000,\n    "tgId": 0,\n    "limitIp": 0,\n    "limitHwid": 0,\n    "enable": true\n  },\n  "inboundIds": [3, 5]\n}',
        response: '{\n  "success": true,\n  "msg": "Client added"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/renewalPreview',
        summary: 'Preview client auto-renewal dates without saving or resetting anything.',
        description:
          'Uses the same calendar and catch-up calculation as auto-renew in the panel timezone. resetWeekday is 1 (Monday) to 7 (Sunday), 0 disables weekly mode; it cannot be combined with positive reset or resetDay. Existing resetDay takes precedence over reset. With expiryTime=0, calendar modes suggest a first cutoff but do not activate renewal. Negative expiryTime waits for first-use activation. resetMax and resetCount simulate the existing per-period allowance limit; the preview is informational and does not reserve an allowance or guarantee node availability.',
        params: [
          {
            name: 'expiryTime',
            in: 'body (json)',
            type: 'integer',
            desc: 'Current cutoff in Unix milliseconds; 0 unlimited, negative first-use duration.',
          },
          {
            name: 'reset',
            in: 'body (json)',
            type: 'integer',
            desc: 'Fixed interval in days; 0 disabled.',
          },
          {
            name: 'resetDay',
            in: 'body (json)',
            type: 'integer',
            desc: 'Monthly calendar day 1-31; 0 disabled.',
          },
          {
            name: 'resetWeekday',
            in: 'body (json)',
            type: 'integer',
            desc: 'Weekly calendar day 1-7 (Monday-Sunday); 0 disabled.',
          },
          {
            name: 'resetMax',
            in: 'body (json)',
            type: 'integer',
            desc: 'Maximum renewals; 0 unlimited.',
          },
          {
            name: 'resetCount',
            in: 'body (json)',
            type: 'integer',
            desc: 'Renewals already consumed; defaults to 0.',
          },
        ],
        responseSchema: 'ClientRenewalPreview',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/update/:email',
        summary:
          'Update an existing client by email. Changes propagate to every attached inbound. Body is the JSON client payload — supply the full set of fields you want to keep (the server replaces the row, it does not patch).',
        description:
          'The inbounds are applied concurrently and independently: one that fails no longer stops the others. Every inbound error names the inbound it came from (`inbound 7: <message>`), and several failures are reported together, one per line. So a `success:false` response can still have applied the edit to the remaining inbounds. The client record is written after the inbounds, so a failure there is reported without an `inbound <id>:` prefix and leaves the inbound edits in place.',
        params: [
          {
            name: 'email',
            in: 'path',
            type: 'string',
            desc: 'Current client email (unique identifier).',
          },
        ],
        body: '{\n  "email": "alice@example.com",\n  "totalGB": 107374182400,\n  "expiryTime": 1767225600000,\n  "limitHwid": 2,\n  "tgId": 123456789,\n  "enable": true\n}',
        response: '{\n  "success": true,\n  "msg": "Client updated"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/del/:email',
        summary:
          'Delete a client by email. Removes it from every attached inbound and drops its traffic record unless keepTraffic=1 is passed.',
        description:
          'The inbounds are applied concurrently and independently: one that fails no longer stops the others. Every inbound error names the inbound it came from (`inbound 7: <message>`), and several failures are reported together, one per line. So a `success:false` response can still have removed the client from the remaining inbounds; the client record is kept in that case, so re-running the call retries exactly the leftovers. The record and traffic rows are dropped after the inbounds, so a failure there is reported without an `inbound <id>:` prefix and leaves the client already removed from every inbound.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          {
            name: 'keepTraffic',
            in: 'query',
            type: 'integer',
            desc: 'Pass 1 to retain the xray_client_traffic row after deletion.',
          },
        ],
        response: '{\n  "success": true,\n  "msg": "Client deleted"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/attach',
        summary: 'Attach an existing client to one or more additional inbounds. Body is JSON.',
        description:
          'A WireGuard client brings its stored `allowedIPs` into the new inbound instead of being given a fresh address, so the call fails with `inbound <id>: wireguard: allowedIPs entry <entry> overlaps <address> used by another client` when its range overlaps an address or prefix a different client of the target inbound holds. Free the address on that inbound first — see POST /panel/api/clients/add for the full rule. Inbounds are applied independently, so the remaining ones are still attached and a `success:false` response can be partial.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          {
            name: 'inboundIds',
            in: 'body (json)',
            type: 'integer[]',
            desc: 'Inbound IDs to attach.',
          },
        ],
        body: '{\n  "inboundIds": [7, 9]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/detach',
        summary: 'Detach a client from one or more inbounds without deleting the client.',
        description:
          'The inbounds are applied concurrently and independently: one that fails no longer stops the others. Every inbound error names the inbound it came from (`inbound 7: <message>`), and several failures are reported together, one per line. So a `success:false` response can still have detached the remaining inbounds. Detach writes nothing beyond the inbounds, so every error carries the prefix.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          {
            name: 'inboundIds',
            in: 'body (json)',
            type: 'integer[]',
            desc: 'Inbound IDs to detach.',
          },
        ],
        body: '{\n  "inboundIds": [5]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/externalLinks',
        summary:
          "Replace a client's external links and external subscriptions. Sends the full set; the server replaces all rows. Disabled rows stay saved for editing but are not emitted in generated subscriptions. The owning client's disabled or expired state also stops these rows from being emitted on future subscription fetches; credentials already imported by an app remain valid until the external provider revokes them.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          {
            name: 'externalLinks',
            in: 'body',
            type: 'object[]',
            desc: "Full replacement list; the server replaces all rows. Each row supports { kind, value, remark, enable, expiryTime, namePrefix }. kind=link: value must be a supported share link such as vless://, vmess://, trojan://, ss://, hysteria2://, or wireguard://, and remark overrides the exported node name. kind=subscription: value must be an http(s) subscription URL, and namePrefix is prepended to fetched node names. Omit enable to default true; enable=false or an expired expiryTime keeps the row saved but excludes it from generated subscriptions. expiryTime is a unix millisecond timestamp where 0 means no link-specific expiry; the owning client's enabled state and expiry still apply. A negative value is rejected. Rows are matched by kind+value across saves, so id is ignored on write. lastFetchAt and lastFetchError are read-only status fields returned by GET.",
          },
        ],
        body: '{\n  "externalLinks": [\n    { "kind": "link", "value": "vless://uuid@host:443?...#srv", "remark": "DE", "enable": true, "expiryTime": 0 },\n    { "kind": "subscription", "value": "https://provider.example/sub/abc", "remark": "Provider", "enable": false, "expiryTime": 1767225600000, "namePrefix": "[zjh] " }\n  ]\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/comment',
        summary:
          "Set a client's remark and change nothing else. The remark is trimmed; an empty one clears it.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
          { name: 'comment', in: 'body', type: 'string', desc: 'The new remark.' },
        ],
        body: '{\n  "comment": "Pays on the 5th"\n}',
        response: '{\n  "success": true,\n  "msg": "Remark saved"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/:email/portal',
        summary:
          'Whether the client can sign in to the subscription server portal ({subPath}portal), and when its password was last set.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'ClientPortalStatus',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/portal',
        summary:
          'Set or replace the client portal password (6 to 72 bytes, stored as a bcrypt hash). Replacing it signs out the client portal sessions.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        body: '{\n  "password": "a-long-passphrase"\n}',
        responseSchema: 'ClientPortalStatus',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/:email/portal/clear',
        summary:
          'Remove the client portal password: the client can no longer sign in and open sessions end.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'ClientPortalStatus',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/resetAllTraffics',
        summary:
          'Reset the up/down counters for every client globally. Quotas and expiry are not affected. Triggers an Xray restart if any counter actually moved.',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/delDepleted',
        summary:
          'Delete every client whose traffic quota is exhausted (used >= total, when reset is disabled) or whose expiry has passed. Returns the deleted count and triggers an Xray restart when any client was on a running inbound.',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 0\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/delOrphans',
        summary:
          'Delete every client that is not attached to any inbound, along with its traffic record, IP log, HWID devices, and external links. Useful for clearing clients left unattached after their inbounds were removed. Returns the deleted count. Cannot be undone.',
        response: '{\n  "success": true,\n  "obj": {\n    "deleted": 0\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/export',
        summary:
          'Return every client as a {client, inboundIds, traffic} array — the shape /import accepts — so the payload round-trips straight back through /import. traffic carries the usage counters (up, down, resetCount, lastOnline, lastSubFetch) and is omitted for a client with no traffic row; the quota itself stays in client.totalGB. Clients with no inbound attachment are included with an empty inboundIds list. The UI shows this in a CodeMirror viewer (copy / download); programmatic callers get the array in obj.',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "client": {\n        "email": "alice@example.com",\n        "id": "...",\n        "totalGB": 53687091200,\n        "expiryTime": 0,\n        "limitHwid": 2,\n        "enable": true,\n        "subId": "..."\n      },\n      "inboundIds": [7, 9],\n      "traffic": {\n        "up": 1048576,\n        "down": 2097152,\n        "resetCount": 0,\n        "lastOnline": 1735680000000\n      }\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/import',
        summary:
          'Import clients from a JSON body { "data": "<json>" }, where data is a string-encoded array produced by /export ([{client, inboundIds, traffic}]). Items with inboundIds are created and attached to those inbounds; items with an empty inboundIds list are restored as unattached client records. An optional traffic object restores the usage counters, only for clients this import creates. Existing emails are never overwritten — they are returned in skipped, and their live counters are left untouched. Triggers a single Xray restart at the end if any target inbound was running; a failure while restoring counters still reports success=false after the clients were created.',
        body: '{\n  "data": "[{\\"client\\":{\\"email\\":\\"alice@example.com\\",\\"enable\\":true},\\"inboundIds\\":[7]}]"\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "created": 2,\n    "skipped": [\n      { "email": "alice@example.com", "reason": "email already in use: alice@example.com" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/renew',
        summary:
          'Renew clients: each expiry moves forward by months or days from the later of now and the current expiry. A month ends on the same date of the next month (UTC+8), on its last day when that month is shorter, and later renewals return to the remembered day: 31 Jan, 28 Feb, 31 Mar. A client without an expiry keeps none, and one whose period starts at first use gets a longer one. resetUsage also zeroes usage and re-enables the client; without it, only a client auto-disabled for expiry or quota is re-enabled, once the renewal lifts it out of depletion. Give exactly one of months (1 to 1200) and days (1 to 36500); on a plan with termMonths only one of its months is taken; otherwise nobody is renewed.',
        body: '{\n  "emails": ["alice", "bob"],\n  "months": 1,\n  "days": 0,\n  "resetUsage": true\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkAdjust',
        summary:
          'Shift expiry and/or traffic quota for many clients in one call. addDays/addBytes may be negative. Clients with unlimited expiry (expiryTime=0) or unlimited traffic (totalGB=0) are skipped for the corresponding field — bulk extend never converts unlimited to limited. A client that was auto-disabled solely because it was depleted (expired or over quota) is automatically re-enabled — locally and on its node — when the adjustment lifts it out of depletion; a manually-disabled or still-depleted client is left disabled. The optional flow directive sets the XTLS flow on every client: "none" clears it, "xtls-rprx-vision"/"xtls-rprx-vision-udp443" set it where the inbound supports it (omit or "" to leave it unchanged). The optional limitHwid sets maximum registered devices (0 = unlimited). The optional adTag sets MTProto Telegram sponsor channel ("none" clears). Returns the adjusted count and per-email skip reasons.',
        body: '{\n  "emails": ["alice", "bob"],\n  "addDays": 30,\n  "addBytes": 53687091200,\n  "flow": "xtls-rprx-vision",\n  "limitHwid": 2,\n  "adTag": "0123456789abcdef0123456789abcdef"\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "adjusted": 2,\n    "skipped": [\n      { "email": "carol", "reason": "unlimited expiry" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkEnable',
        summary:
          'Enable many clients in one call. Emails are grouped by inbound and applied with a single read-modify-write per inbound; the running Xray (local or remote node) is updated to add each user. Note that enabling a client whose quota is exhausted or whose expiry has passed only flips the flag — the traffic loop will disable it again on the next tick. Returns the changed count and per-email skip reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "changed": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkDisable',
        summary:
          'Disable many clients in one call. Emails are grouped by inbound and applied with a single read-modify-write per inbound; the running Xray (local or remote node) is updated to remove each user. Returns the changed count and per-email skip reasons.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "changed": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkDel',
        summary:
          'Delete many clients in one call. The server processes the list sequentially so each delete sees the committed state of the previous one — avoids the race the per-email fan-out had on the panel side. Pass keepTraffic=true to retain the xray_client_traffic rows after deletion.',
        body: '{\n  "emails": ["alice", "bob"],\n  "keepTraffic": false\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "deleted": 2,\n    "skipped": [\n      { "email": "carol", "reason": "client not found" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkCreate',
        summary:
          'Create many clients in one call. Body is a JSON array of {client, inboundIds} payloads — the same shape /add accepts. Items are processed sequentially; per-email skip reasons are returned for items that fail (e.g., duplicate email). Triggers a single Xray restart at the end if any inbound was running.',
        body: '[\n  {\n    "client": {\n      "email": "alice@example.com",\n      "totalGB": 53687091200,\n      "expiryTime": 0,\n      "limitHwid": 2,\n      "enable": true\n    },\n    "inboundIds": [7]\n  },\n  {\n    "client": {\n      "email": "bob@example.com",\n      "totalGB": 53687091200,\n      "expiryTime": 0,\n      "limitHwid": 0,\n      "enable": true\n    },\n    "inboundIds": [7, 9]\n  }\n]',
        response:
          '{\n  "success": true,\n  "obj": {\n    "created": 2,\n    "skipped": [\n      { "email": "alice@example.com", "reason": "email already in use" }\n    ]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkAttach',
        summary:
          'Attach many existing clients to many inbounds in one call. Each client keeps its identity (email/UUID/password/subId) and a shared traffic row; all clients are added to a target inbound in a single AddInboundClient call. Clients already present on a target are reported under skipped. Returns per-email attached/skipped/errors lists and triggers a single Xray restart if any target inbound was running.',
        params: [
          {
            name: 'emails',
            in: 'body (json)',
            type: 'string[]',
            desc: 'Emails of existing clients to attach.',
          },
          {
            name: 'inboundIds',
            in: 'body (json)',
            type: 'integer[]',
            desc: 'Target inbound IDs to attach every client to.',
          },
        ],
        body: '{\n  "emails": ["alice", "bob"],\n  "inboundIds": [7, 9]\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "attached": ["alice", "bob"],\n    "skipped": ["bob"],\n    "errors": []\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkDetach',
        summary:
          "Mirror of bulkAttach: detach many existing clients from many inbounds in one call. For each email, intersects the client's current inbounds with the requested set and detaches from those only; (email, inbound) pairs where the client is not currently attached are silently no-ops. Emails not attached to any of the requested inbounds are reported under skipped. Client records are kept even if they become orphaned — use bulkDel for full removal. Returns per-email detached/skipped/errors lists and triggers a single Xray restart if any target inbound was running.",
        params: [
          {
            name: 'emails',
            in: 'body (json)',
            type: 'string[]',
            desc: 'Emails of existing clients to detach.',
          },
          {
            name: 'inboundIds',
            in: 'body (json)',
            type: 'integer[]',
            desc: 'Inbound IDs to detach the clients from.',
          },
        ],
        body: '{\n  "emails": ["alice", "bob"],\n  "inboundIds": [7, 9]\n}',
        response:
          '{\n  "success": true,\n  "obj": {\n    "detached": ["alice", "bob"],\n    "skipped": [],\n    "errors": []\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/bulkResetTraffic',
        summary:
          'Zero up/down counters for many clients in one call. Loops the single-reset path so each client is re-enabled across its attached inbounds and pushed to Xray/remote nodes. Returns the count of successfully reset clients.',
        body: '{\n  "emails": ["alice", "bob"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/resetTraffic/:email',
        summary:
          'Zero out a single client’s up/down counters. Re-enables the client across every attached inbound and pushes the change to Xray (or the remote node) so depleted users can connect again immediately.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/clients/updateTraffic/:email',
        summary:
          'Manually adjust a client’s upload + download counters. Useful for migrations from external accounting systems.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        body: '{\n  "upload": 1073741824,\n  "download": 5368709120\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/ips/:email',
        summary:
          'List the source IPs seen with the given client’s credentials in the last 30 minutes, each with the node it connected through. For the IPs online right now and the IP limit, use onlineIps.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'ClientIpInfo',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/clients/clearIps/:email',
        summary: 'Reset the recorded IP list for a client.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/clients/onlineIps/:email',
        summary:
          'How many of the client’s IP-limit slots are in use right now (`count` of `limit`, 0 = no limit), the networks it is online from (an IPv4 /24 or an IPv6 /64 with the addresses seen; just the address when its range holds a server or allowlisted address) with the servers they connect to, and its running bans. Relay and other server addresses never appear; allowlisted networks are listed with `counted: false`. Taken from the last 10-second scan.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'ClientOnlineIps',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/unbanIp/:email',
        summary:
          'Lift one IP-limit ban before it expires. Agents drop the rule on their next config sync; the panel’s own core gets a hot apply.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
          {
            name: 'network',
            in: 'body',
            type: 'string',
            desc: 'The banned network, exactly as the ban lists it.',
          },
        ],
        body: '{\n  "network": "198.51.100.7"\n}',
        response: '{\n  "success": true,\n  "msg": "Ban lifted"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/hwids/:email',
        summary:
          'List registered HWID devices for a client with a short fingerprint. Full hashes are not exposed.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "firstSeen": 1735000000000,\n      "lastSeen": 1735100000000,\n      "userAgent": "Happ/1.0",\n      "deviceOs": "android",\n      "osVersion": "15",\n      "deviceModel": "Pixel 9",\n      "fingerprint": "6ad17c93e821"\n    }\n  ]\n}',
      },
      {
        method: 'DELETE',
        path: '/panel/api/clients/hwids/:email',
        summary:
          'Clear all registered HWID devices for a client so new devices can register again.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
      },
      {
        method: 'DELETE',
        path: '/panel/api/clients/hwids/:email/:id',
        summary:
          'Remove a single registered HWID device by its id, freeing one slot under the HWID limit.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email.' },
          { name: 'id', in: 'path', type: 'number', desc: 'Device id, from the list endpoint.' },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/clients/onlines',
        summary:
          'List the emails of currently connected clients (last seen within the heartbeat window), deduped across every node.',
        response: '{\n  "success": true,\n  "obj": ["user1", "user2"]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/onlinesByGuid',
        summary:
          'Online client emails grouped by the panelGuid of the node that physically hosts each client. The local panel uses its own GUID; each node (at any depth in a chain) uses its GUID. Lets the inbounds page attribute online status to the real node instead of the intermediate one it syncs through.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "a1b2-...": ["user1"],\n    "c3d4-...": ["user1", "user2"]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/clientIpsByGuid',
        summary:
          'Per-client source IPs grouped by the panelGuid of the node that observed them. Lets the central panel attribute and enforce per-client IP limits using the real visitor IPs each node sees, instead of the address of the intermediate panel it syncs through.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "a1b2-...": {\n      "user1": [\n        { "ip": "1.2.3.4", "timestamp": 1700000000 }\n      ]\n    }\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/activeInbounds',
        summary:
          "Inbound tags that carried traffic within the heartbeat window, grouped by the hosting node's panelGuid. Pairs with onlinesByGuid so the inbounds page only marks a multi-inbound client online on the inbounds it actually used. Nodes that do not report per-inbound activity are absent.",
        response:
          '{\n  "success": true,\n  "obj": {\n    "a1b2-...": ["in-443-tcp", "in-8443-tcp"]\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/lastOnline',
        summary: 'Map of client email → last-seen unix timestamp.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "user1": 1700000000,\n    "user2": 1699999000\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/traffic/:email',
        summary: 'Traffic counters for a client identified by email.',
        params: [
          {
            name: 'email',
            in: 'path',
            type: 'string',
            desc: 'Client email (unique across the panel).',
          },
        ],
        responseSchema: 'ClientTraffic',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/subLinks/:subId',
        summary:
          'Return every protocol URL (vless://, vmess://, trojan://, ss://, hysteria://, hy2://) for clients matching the subscription ID. Same result set as the configured subPath endpoint, but as a JSON array — no base64. When an inbound has streamSettings.externalProxy set, one URL is emitted per external proxy. Empty array when the subId has no enabled clients.',
        params: [
          {
            name: 'subId',
            in: 'path',
            type: 'string',
            desc: "Subscription ID, taken from the client's subId field.",
          },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?security=reality&...#user1",\n    "vmess://eyJ2IjoyLC..."\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/clients/happLink/:id',
        summary:
          'Generate a fresh Happ crypt5 link locally from the current client subscription URL when Happ link generation is enabled. The panel applies a resource limit of 8192 UTF-8 bytes to the source URL; this is not a Happ client maximum. Longer sources return success: false with msg: happ_source_too_long and obj: null. The source URL is not sent to a generation provider, and the result is not stored or reused.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Stable client record ID.' }],
        responseSchema: 'HappLinkResult',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/links/:email',
        summary:
          'Return every URL for one client across all attached inbounds, one per advertised endpoint: the managed hosts of the inbound, else its streamSettings.externalProxy entries, else its own address. Supported protocols: vmess, vless, trojan, shadowsocks, hysteria, mtproto. Protocols without a URL form (socks, http, mixed, wireguard, dokodemo, tunnel) contribute nothing.',
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        response:
          '{\n  "success": true,\n  "obj": [\n    "vless://uuid@host:443?...#user1"\n  ]\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/clients/socks5/:email',
        summary:
          'List the SOCKS5 logins of one client: every attached `mixed` inbound with the address and port a share link would advertise (the node address and NAT port, or the host this request reached for an inbound on the panel itself), the client name as username and its SOCKS5 password, and the exit node when the inbound relays on through a chain. Subscriptions never carry these.',
        params: [{ name: 'email', in: 'path', type: 'string', desc: 'Client email.' }],
        responseSchema: 'Socks5Proxy',
        responseSchemaArray: true,
      },
    ],
  },

  {
    id: 'nodes',
    title: 'Nodes',
    description:
      'Manage remote 3x-ui panels acting as nodes for a central panel. All endpoints under /panel/api/nodes.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/nodes/list',
        summary:
          'List every configured node with its connection details, health, and last heartbeat patch.',
        responseSchema: 'NodeView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/mtls/ca',
        summary:
          "This panel's node-auth CA certificate (public, PEM) to paste into a node's mTLS trust setting. Lazily mints the CA and the master client cert on first call. Pair with setting tlsVerifyMode=mtls on the node.",
        response:
          '{\n  "success": true,\n  "obj": {\n    "caCert": "-----BEGIN CERTIFICATE-----\\n...\\n-----END CERTIFICATE-----\\n"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/mtls/trustCA',
        summary:
          "Set the CA certificate this panel trusts for incoming node-API client certificates (this panel acting as a node). Paste the managing panel's CA (from nodes/mtls/ca). An empty caCert disables it. A non-empty value must be a PEM certificate. Applied on the next panel restart.",
        body: '{\n  "caCert": "-----BEGIN CERTIFICATE-----\\n...\\n-----END CERTIFICATE-----\\n"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/mtls/reloadClient',
        summary:
          'Validate the stored master mTLS client credential and invalidate cached transports. Each transport closes its old idle pool and rebuilds with the rotated certificate before its next request.',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/get/:id',
        summary: 'Fetch a single node by ID.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
        responseSchema: 'NodeView',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/webCert/:id',
        summary:
          'Fetch a node\'s own web TLS certificate/key file paths (proxied to the node). Used by the inbound form\'s "Set Cert from Panel" so a node-assigned inbound gets paths that exist on the node, not the central panel.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
        response:
          '{\n  "success": true,\n  "obj": {\n    "webCertFile": "/root/cert/example.com/fullchain.pem",\n    "webKeyFile": "/root/cert/example.com/privkey.pem"\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/add',
        summary:
          'Register a new node. kind "panel" (default) is a remote 3x-ui reached at its URL with a write-only apiToken. kind "agent" dials in with the secret from nodes/agentSecret; address may be blank and is filled from its first authenticated public connection. An optional probeServerId creates the host and its Lite link atomically; the server must exist and be unlinked. An agent may take a trafficMultiplier from 0 to 100 (default 1): what each byte users move through it counts as toward their quotas; a panel node always counts 1. Responses expose hasApiToken only.',
        body: '{\n  "name": "de-fra-1",\n  "kind": "panel",\n  "remark": "",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef...",\n  "clearApiToken": false,\n  "enable": true,\n  "allowPrivateAddress": false\n}',
        responseSchema: 'NodeView',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/update/:id',
        summary:
          'Replace a node\u2019s connection details. apiToken is write-only: omit it or send an empty string to keep the stored token; set clearApiToken=true to clear it. Omit trafficMultiplier to keep an agent\u2019s multiplier; a panel node is reset to 1.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
        body: '{\n  "name": "de-fra-1",\n  "remark": "",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "",\n  "clearApiToken": false,\n  "enable": true,\n  "allowPrivateAddress": false\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/del/:id',
        summary: 'Delete a node. Inbounds bound to it are not auto-migrated.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/setEnable/:id',
        summary: 'Pause or resume traffic sync with this node.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
        body: '{\n  "enable": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/test',
        summary:
          'Probe a node without saving it. Uses the body as connection details and returns the same heartbeat snapshot a registered node would have.',
        body: '{\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef..."\n}',
        responseSchema: 'ProbeResultUI',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/certFingerprint',
        summary:
          "Connect to the node over HTTPS without verifying its certificate and return the leaf certificate's SHA-256 (base64). Used by the Add/Edit Node dialog to fetch and pin a self-signed certificate. Uses the same body as /test.",
        body: '{\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/"\n}',
        response: '{\n  "success": true,\n  "obj": "k3b1...base64-sha256...="\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/inbounds',
        summary:
          'Use unsaved node connection details to list the remote inbounds available for selective import.',
        body: '{\n  "name": "de-fra-1",\n  "scheme": "https",\n  "address": "node1.example.com",\n  "port": 2053,\n  "basePath": "/",\n  "apiToken": "abcdef..."\n}',
        response:
          '{\n  "success": true,\n  "obj": [\n    { "tag": "inbound-443", "remark": "VLESS", "protocol": "vless", "port": 443 }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/probe/:id',
        summary: 'Probe an existing node, updating its cached health state.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/updatePanel',
        summary:
          'Trigger the official panel self-updater on each given node (downloads the latest release and restarts). Only enabled, online nodes are updated; offline/disabled ones are reported as skipped. Set "dev": true to move the nodes to the rolling per-commit dev channel instead of the latest stable release. Returns a per-node result list.',
        body: '{\n  "ids": [1, 2, 3],\n  "dev": false\n}',
        response:
          '{\n  "success": true,\n  "obj": [\n    { "id": 1, "name": "de-1", "ok": true },\n    { "id": 2, "name": "fr-1", "ok": false, "error": "node is offline" }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/agentSecret/:id',
        summary:
          'Mint a new secret for an agent node and return it once; only its hash is stored. The old secret stops working at once and the agent connected with it is dropped. Panel nodes have no agent secret and are refused.',
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node ID.' }],
        responseSchema: 'AgentSecretView',
      },
      {
        method: 'GET',
        path: '/agent/install.sh',
        summary: 'Download the installer for the running panel version.',
        description:
          'Public shell script. Run as root with the panel origin and a host secret. Supports Linux amd64/arm64 with systemd or OpenRC; downloads and verifies the matching agent package and the panel’s geodata. Available at the origin root independently of the administrator base path.',
        security: [],
        responses: {
          '200': {
            description: 'POSIX shell installer',
            content: { 'text/x-shellscript': { schema: { type: 'string' } } },
          },
        },
      },
      {
        method: 'GET',
        path: '/agent/download/:version/:asset',
        summary: 'Download a versioned agent package, checksum or geodata using a host secret.',
        description:
          'Authorization: Bearer must contain an enabled agent host’s secret. Administrator API tokens do not authorize this route. Version must equal the running panel; unknown versions and assets return 404. Available at the origin root independently of the administrator base path.',
        params: [
          { name: 'version', in: 'path', type: 'string', desc: 'Running panel version.' },
          {
            name: 'asset',
            in: 'path',
            type: 'string',
            enum: [
              'linux-amd64.tar.gz',
              'linux-arm64.tar.gz',
              'linux-amd64.sha256',
              'linux-arm64.sha256',
              'geoip.dat',
              'geosite.dat',
            ],
          },
        ],
        responses: {
          '200': {
            description: 'Agent artifact',
            content: {
              'application/octet-stream': { schema: { type: 'string', format: 'binary' } },
            },
          },
        },
      },
      {
        method: 'POST',
        path: '/panel/api/nodes/restartXray/:id',
        summary:
          "Restart one host's Xray through its runtime: an agent restarts its embedded core, a panel node its own Xray process. Refused for a disabled host.",
        params: [{ name: 'id', in: 'path', type: 'number', desc: 'Node id.' }],
        response: '{\n  "success": true,\n  "msg": "Xray restarted"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/nodes/history/:id/:metric/:bucket',
        summary:
          'Aggregated metric history for a node — same shape as /server/history, scoped to one node.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Node ID.' },
          { name: 'metric', in: 'path', type: 'string', desc: 'cpu | mem.' },
          {
            name: 'bucket',
            in: 'path',
            type: 'number',
            desc: 'Bucket size in seconds. Allowed: 2, 30, 60, 120, 180, 300.',
          },
        ],
      },
    ],
  },

  {
    id: 'rule-templates',
    title: 'Rule templates',
    description:
      "Clash rule templates plans share: rule lines, a YAML document (whose proxy groups list __PROXY_NODES__ where the subscription's nodes go, or select them with a filter) or one HTTPS URL. A variant (baseId set) holds only what it changes in a full YAML template, as a Clash Verge Merge document: a top-level key replaces the base's, prepend-rules / append-rules and prepend-proxy-groups / append-proxy-groups add before or after the base's lists. A plan names its template by id; plans with templateId 0 and clients without a plan get the default template. Each content change is kept as a version, the newest 20 per template.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/ruleTemplates/list',
        summary:
          'List the templates without their content: kind (rules, yaml or remote), size in bytes, whether it is the default, how many plans use it (the default also counts the plans that name none), and for a variant its base and the keys it changes.',
        responseSchema: 'RuleTemplateSummary',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/ruleTemplates/get/:id',
        summary: 'Get one template with its content.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
        responseSchema: 'RuleTemplate',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/add',
        summary:
          'Create a template. Names are unique. Content that could not render is refused: broken YAML, a URL with credentials or not HTTPS, or proxy groups none of which lists __PROXY_NODES__ or a filter. With baseId it is a variant: its content must be a YAML map, the base a full YAML template, and the merge is checked as above.',
        body: '{\n  "name": "alpha_v3",\n  "content": "prepend-rules:\\n  - DOMAIN-SUFFIX,example.com,DIRECT\\n",\n  "baseId": 1\n}',
        responseSchema: 'RuleTemplate',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/update/:id',
        summary:
          'Rename a template or replace its content, checked as on create. A changed content is kept as a new version. A base must stay a YAML document its variants merge onto, and cannot become a variant itself.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
        body: '{\n  "name": "alpha_v3",\n  "content": "DOMAIN-SUFFIX,example.com,DIRECT"\n}',
        responseSchema: 'RuleTemplate',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/del/:id',
        summary:
          'Delete a template and its versions. Refused for the default template, for a base with variants, and while any plan uses it.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/setDefault/:id',
        summary:
          'Make the template the default: the one plans without their own, and clients without a plan, get.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
      },
      {
        method: 'GET',
        path: '/panel/api/ruleTemplates/versions/:id',
        summary: "List the template's kept versions, newest first, without their content.",
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
        responseSchema: 'RuleTemplateVersionView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/restore/:versionId',
        summary:
          "Put a kept version back as its template's content; the restore is itself a new version.",
        params: [{ name: 'versionId', in: 'path', type: 'integer', desc: 'Version id.' }],
        responseSchema: 'RuleTemplate',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/preview',
        summary:
          "Render the Clash config the plan's first member would get with this content as their template, checked as on save; with baseId the content is a variant merged onto that template. ruleSets, by name, tries proposed rules in place of those rule sets' saved ones, checked as a rule set save would check them. Refused for a plan without members.",
        body: '{\n  "planId": 1,\n  "content": "RULE-SET,ai,PROXY\\nMATCH,DIRECT",\n  "baseId": 0,\n  "ruleSets": { "ai": "DOMAIN-SUFFIX,example.com" }\n}',
        responseSchema: 'RuleTemplatePreview',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleTemplates/variantOf/:id',
        summary:
          "Keep a full YAML template as a variant of baseId, holding only what differs. A template identical to the base is folded into it instead: its plans and default star move to the base and it is deleted. Rules added between the base's own can only move to the front: moved counts them, and applying then needs allowReorder. Without apply nothing changes and the answer says what would. Refused for a template without a key the base sets, or one whose differences would not read back the same.",
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Template id.' }],
        body: '{\n  "baseId": 1,\n  "allowReorder": false,\n  "apply": false\n}',
        responseSchema: 'RuleTemplateConversion',
      },
    ],
  },
  {
    id: 'rule-sets',
    title: 'Rule sets',
    description:
      "Rule lists templates use as RULE-SET,<name>,<target> without declaring a rule-provider: the subscription writes the set's rules in that line's place, each sent to its target, so clients fetch nothing extra. RULE-SET,<name>:<provider>,<target> takes only the rules under that provider's headings. A set holds classical rules without targets (DOMAIN, DOMAIN-SUFFIX, DOMAIN-KEYWORD, DOMAIN-REGEX, IP-CIDR, IP-CIDR6, PROCESS-NAME), as plain lines or a payload list, with '# Provider / Tier' headings. A set may follow an upstream list: a daily check at 10:00 (UTC+8) fetches it and scores what changed since the last review, and the Telegram bot tells the admins when a review is due. Fetching never changes what subscriptions get; only a save does. Each save is kept as a version, the newest 30 per set.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/ruleSets/list',
        summary:
          'List the sets without their lists: how many rules each serves, when they were saved and reviewed, the last fetch and any failures in a row, and since when upstream has changes not yet reviewed.',
        responseSchema: 'RuleSetSummary',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/ruleSets/get/:name',
        summary:
          "Get one set: the rules it serves, the upstream list as last reviewed and as last fetched, the latest list's hash for a save to name, and what upstream added and removed between the two, with risks flagged.",
        params: [{ name: 'name', in: 'path', type: 'string', desc: 'Rule set name, e.g. ai.' }],
        responseSchema: 'RuleSetDetail',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleSets/update/:name',
        summary:
          'Save a review: the rules the set serves from now on, checked as a client would read them (no IP-ASN, options only no-resolve on IP rules, no repeats, at most 5000). reviewedHash, the latestHash the review read, marks that upstream list reviewed; one naming an older list is refused, since the review has not seen what changed after it. Without reviewedHash the reviewed list stays as it was. Kept as a version.',
        params: [{ name: 'name', in: 'path', type: 'string', desc: 'Rule set name.' }],
        body: '{\n  "rules": "# Example AI / Core\\nDOMAIN-SUFFIX,example.com",\n  "reviewedHash": "a3f1c2",\n  "note": "Example AI added"\n}',
        responseSchema: 'RuleSetSummary',
      },
      {
        method: 'GET',
        path: '/panel/api/ruleSets/versions/:name',
        summary: "List the set's kept versions, newest first, without their lists.",
        params: [{ name: 'name', in: 'path', type: 'string', desc: 'Rule set name.' }],
        responseSchema: 'RuleSetVersionView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/ruleSets/restore/:versionId',
        summary:
          'Put a kept version back: its rules and the upstream list it reviewed, so what upstream changed since then is pending again. The restore is itself a new version.',
        params: [{ name: 'versionId', in: 'path', type: 'integer', desc: 'Version id.' }],
        responseSchema: 'RuleSetSummary',
      },
      {
        method: 'POST',
        path: '/panel/api/ruleSets/check',
        summary:
          "Fetch every set's upstream list now, as the daily check does, and answer what changed since each review: per set the new and gone providers, the rules added and removed, and the score; overall the score with the days pending, the threshold, and whether a review is due.",
        responseSchema: 'RuleSetWatch',
      },
    ],
  },
  {
    id: 'plans',
    title: 'Plans',
    description:
      'What a group of clients shares: the inbounds (servers) they get, the rule template their Clash subscriptions use, and an IP limit. Each client keeps their own quota, expiry and reset schedule; assigning a plan stamps its IP limit and attaches/detaches inbounds so the client sits on exactly the plan inbounds.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/plans/nodeOptions',
        summary:
          'List selectable direct and relayed node variants for plans and proxy-group assignments.',
      },

      {
        method: 'GET',
        path: '/panel/api/plans/codes/:id',
        summary: 'List activation codes for a plan, newest first. Plan id 0 lists every code.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Plan id, or 0 for all plans.' }],
        responseSchema: 'ActivationCode',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/plans/codes/add',
        summary:
          'Create 1 to 200 single-use activation codes for a plan with nodes. Each grants the quota in bytes, a validity of calendar months or of days counted from creation (including unused time), and monthly reset day. Expired codes cannot be redeemed; a renewal adds the months to the same date (or the days), worth the share of its validity left when the code is used. Zero quota means unlimited, months and days both 0 never expire, reset day 0 never resets. A plan with termMonths only takes one of its months.',
        body: '{\n  "planId": 1,\n  "count": 5,\n  "totalGB": 107374182400,\n  "months": 1,\n  "days": 0,\n  "resetDay": 22,\n  "note": "March group"\n}',
        responseSchema: 'ActivationCode',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/plans/codes/del/:id',
        summary:
          'Revoke an unused code or delete a used code from the history. A used code never becomes reusable.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Code id.' }],
        response: '{\n  "success": true,\n  "obj": {\n    "id": 1\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/plans/list',
        summary: 'List every plan with the inbounds it grants and how many clients use it.',
        responseSchema: 'PlanSummary',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/plans/add',
        summary:
          "Create a plan. Inbound ids must exist. templateId names the rule template members' Clash subscriptions use; 0 leaves them on the default template. limitIp 0 means no IP limit. termMonths, 1 to 1200 calendar months each, are the only terms the plan is sold and renewed by (renewals and activation codes); none allows any.",
        body: '{\n  "name": "Monthly 100G",\n  "limitIp": 0,\n  "remark": "",\n  "templateId": 0,\n  "inboundIds": [1, 2],\n  "termMonths": [3, 12]\n}',
        responseSchema: 'Plan',
      },
      {
        method: 'POST',
        path: '/panel/api/plans/update/:id',
        summary:
          'Replace a plan. Every client on the plan is attached to the inbounds the plan gained and detached from those it lost; their other inbounds stay. With applyToMembers, the plan IP limit is also re-stamped onto them; their quota, expiry and usage are left alone. termMonths replaces the terms of the plan; leaving it out allows any.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Plan id.' }],
        body: '{\n  "name": "Monthly 200G",\n  "limitIp": 2,\n  "remark": "",\n  "templateId": 2,\n  "inboundIds": [1, 2],\n  "applyToMembers": true\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "id": 1\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/plans/del/:id',
        summary:
          'Delete a plan. Refused while any client is on it — move or unassign those clients first.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Plan id.' }],
        response: '{\n  "success": true,\n  "obj": {\n    "id": 1\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/plans/assign',
        summary:
          'Put clients on a plan. Each gets the plan IP limit and exactly the plan inbounds; their own quota, expiry and reset schedule stay as they are.',
        body: '{\n  "emails": ["alice", "bob"],\n  "planId": 1\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 2\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/plans/unassign',
        summary:
          'Take clients off their plan. Their current quota, expiry and inbounds stay as they are.',
        body: '{\n  "emails": ["alice"]\n}',
        response: '{\n  "success": true,\n  "obj": {\n    "affected": 1\n  }\n}',
      },
    ],
  },

  {
    id: 'traffic',
    title: 'Traffic',
    description:
      "The home page's traffic overview. Quotas and billing-cycle use come from the Lite probe for the hosts linked to it (node id 0 is the panel itself); a host without a quota counts as unlimited, and a failed probe leaves every host unlinked with the reason in servers.error. The daily history behind the chart and the rankings is recorded every 10 minutes in the panel time zone.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/traffic/overview',
        summary:
          "Get the servers' quotas and use, 30 days of traffic, and the hosts and clients ranked by what they used in the period (the user ranking lists the busiest 100; users counts every client).",
        params: [
          {
            name: 'period',
            in: 'query',
            type: 'string',
            desc: 'today, week (from Monday) or month (from the 1st), in the panel time zone. Defaults to month; any other value is refused.',
            optional: true,
            defaultValue: 'month',
          },
        ],
        responseSchema: 'TrafficOverview',
      },
    ],
  },
  {
    id: 'ai-usage',
    title: 'AI Usage',
    description:
      "Claude Code and Codex usage reported by Pigger Switch from each computer. A report covers whole days of the computer's time zone and replaces what the panel held for them, so resending is safe. Costs are API-price equivalents in USD; input tokens exclude cache reads. An ai-usage API token may call the ingest endpoint and nothing else.",
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/aiUsage/ingest',
        summary:
          "Upload one computer's usage for the days from..to, its sessions (upserted; with sessionsSince set, the computer's sessions active since then that the report leaves out are deleted) and its latest plan-limit readings. A reading may carry estimates: each window's usage, its estimated limit (usage ÷ reported share) and past windows; a reading without them clears the old ones.",
        requestSchema: { $ref: '#/components/schemas/AiUsageReport' },
        responseSchema: 'AiUsageIngestResult',
        errorResponse:
          '{\n  "success": false,\n  "msg": "day 2026-10-07 is outside the report\'s window 2026-10-05..2026-10-06"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/aiUsage/overview',
        summary:
          "Get the period's totals and project/model rankings (busiest 100 by cost), 30 days of daily cost and tokens per app, the period's 50 costliest sessions, each tool's newest plan-limit reading with its window estimates, and the reporting computers with their spend.",
        params: [
          {
            name: 'period',
            in: 'query',
            type: 'string',
            desc: 'today, week (from Monday), month (from the 1st) or all, in the panel time zone. Defaults to month.',
            optional: true,
            defaultValue: 'month',
          },
          {
            name: 'deviceId',
            in: 'query',
            type: 'integer',
            desc: 'Only this computer; 0 or absent for all of them.',
            optional: true,
          },
          {
            name: 'app',
            in: 'query',
            type: 'string',
            desc: "claude or codex (the page shows one tool at a time); all or absent for both. Also scopes each computer's spend.",
            optional: true,
          },
        ],
        responseSchema: 'AiUsageOverview',
      },
      {
        method: 'POST',
        path: '/panel/api/aiUsage/devices/delete/:id',
        summary: 'Forget a computer and everything it reported.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Device id.' }],
        response: '{\n  "success": true\n}',
      },
    ],
  },
  {
    id: 'probe',
    title: 'Probe',
    description:
      "Server status read from a Lite monitor running on the panel's own host. The panel asks Lite over loopback without a credential, so it shows Lite's guest view: a server hidden in Lite is hidden here, and a private Lite site cannot be read. Answers are cached for 2 seconds; while Lite fails, the last good answer is served for up to 90 seconds, marked stale. Links say which Lite server is which panel host (node id 0 is the panel itself); the client portal uses them to show each client only the servers behind its own inbounds.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/probe/servers',
        summary:
          'List every server Lite reports with its status (online, offline or unknown), its metrics while online, and the panel host it is linked to. A Lite failure is not a failed request: success stays true and error carries the reason.',
        responseSchema: 'ProbeOverview',
      },
      {
        method: 'GET',
        path: '/panel/api/probe/links',
        summary:
          'List the panel itself (node id 0) and every node with the Lite server each is linked to. serverName is empty when the link points at a server Lite no longer lists.',
        responseSchema: 'ProbeLinkView',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/probe/links',
        summary:
          'Replace the whole set of links. nodeId must be 0 or an existing node and may appear once; a serverId (at most 64 characters) may be linked to one host only; an empty serverId leaves that host unlinked. Returns the new list.',
        body: '{\n  "links": [\n    { "nodeId": 0, "serverId": "00000000-0000-4000-8000-000000000002" },\n    { "nodeId": 2, "serverId": "00000000-0000-4000-8000-000000000001" },\n    { "nodeId": 3, "serverId": "" }\n  ]\n}',
        requestSchema: { $ref: '#/components/schemas/ProbeLinksInput' },
        responseSchema: 'ProbeLinkView',
        responseSchemaArray: true,
      },
      {
        method: 'GET',
        path: '/panel/api/probe/settings',
        summary:
          'Get the Lite address the panel reads and the public status page the Probe page links to. An empty url means the probe is off.',
        responseSchema: 'ProbeSettings',
      },
      {
        method: 'POST',
        path: '/panel/api/probe/settings',
        summary:
          'Save both settings. url must be empty or http(s)://<literal loopback IP>[:port] (127.0.0.0/8 or [::1]) with no path, query, fragment or credentials; publicUrl must be empty or an http(s) URL. Returns the values as stored.',
        body: '{\n  "url": "http://127.0.0.1:27777",\n  "publicUrl": "https://probe.example.com"\n}',
        requestSchema: { $ref: '#/components/schemas/ProbeSettings' },
        responseSchema: 'ProbeSettings',
      },
    ],
  },
  {
    id: 'abuse',
    title: 'Abuse',
    description:
      "Abuse detection: each server (node id 0 is the panel's own core) is off, observe or enforce. A detecting server watches its users' connections and traffic, never their content, for spam, BitTorrent, scanning, floods, crawling, repeated speed tests and hours at full speed. Observe only records and tells the admins; enforce does what each rule is set to. Each strike bans the account on every server for 30 minutes; the fourth within 30 days locks it until an admin lifts it, which also clears its strikes. IP-limit bans are kept in the same history but never count as strikes. The panel also guards sign-ups on the user page per network.",
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/abuse/overview',
        summary:
          'Every server with its mode (capable is false for an agent too old to detect, or not connected), the settings, the abuse bans and locks running now, and the latest 100 hits with what was done about each: observed, noticed (recorded for the admins), warned, banned, locked, held (a hit during a ban) or blocked (a network refused sign-ups for the day).',
        responseSchema: 'AbuseOverview',
      },
      {
        method: 'POST',
        path: '/panel/api/abuse/mode',
        summary:
          "Set one server's mode: off, observe or enforce. The panel's own core changes at once, an agent on its next config sync. Returns the overview.",
        body: '{\n  "nodeId": 3,\n  "mode": "observe"\n}',
        responseSchema: 'AbuseOverview',
      },
      {
        method: 'POST',
        path: '/panel/api/abuse/settings',
        summary:
          "Replace the abuse settings. rules are the thresholds every detecting server checks; a zero turns a check off, and windows are minutes kept within the hour a server remembers. actions say what each rule's hits do on an enforcing server: record (tell the admins), warn (also tell the person, no strike) or ban (a strike); the full-speed rule's first stage only ever warns. signup limits sign-ups on the user page per network (IPv4 /24, IPv6 /48) and 24 hours: reaching limit is reported to the admins, and while action is ban the network is refused until the day is over; limit 0 turns it off, action is record or ban. Returns the overview.",
        requestSchema: { $ref: '#/components/schemas/AbuseSettings' },
        responseSchema: 'AbuseOverview',
      },
      {
        method: 'GET',
        path: '/panel/api/abuse/history/:email',
        summary:
          "One account's standing (its running ban or lock, and its strikes within 30 days) and its bans of either kind from the last 90 days, newest first.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        responseSchema: 'AbuseHistory',
      },
      {
        method: 'POST',
        path: '/panel/api/abuse/lift/:email',
        summary:
          "End the account's running abuse ban now. Ending a lock also clears the account's strikes; ending a 30-minute ban keeps them. Returns the account's history.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        responseSchema: 'AbuseHistory',
      },
      {
        method: 'POST',
        path: '/panel/api/abuse/forgive/:email',
        summary:
          "Clear the account's strikes; its history keeps the bans, marked forgiven. Returns the account's history.",
        params: [
          { name: 'email', in: 'path', type: 'string', desc: 'Client email (unique identifier).' },
        ],
        responseSchema: 'AbuseHistory',
      },
    ],
  },
  {
    id: 'backup',
    title: 'Backup',
    description: 'Operations that interact with the configured Telegram bot.',
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/backuptotgbot',
        summary:
          'Send a fresh DB backup to every Telegram chat configured as an admin recipient. No body, no params.',
      },
    ],
  },

  {
    id: 'settings',
    title: 'Settings',
    description:
      'Panel configuration and user credentials. All endpoints live under /panel/api/setting and require a logged-in session or Bearer token.',
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/setting/all',
        summary:
          'Return every panel setting: web server, Telegram bot, subscription, security, LDAP. The full JSON blob that the Settings page edits.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "webPort": 2053,\n    "webCertFile": "",\n    "webKeyFile": "",\n    "webBasePath": "/",\n    "subPort": 10882,\n    "subPath": "/sub/",\n    "subClashAutoDetect": false,\n    "subClashUserAgentRegex": "",\n    "subJsonEnable": false,\n    "subJsonAutoDetect": false,\n    "subJsonAlwaysArray": false,\n    "subJsonUserAgentRegex": "",\n    "subJsonPath": "/json/",\n    "subJsonURI": "https://sub.example.com/json/",\n    "subClashEnable": true,\n    "subClashPath": "/clash/",\n    "subClashURI": "https://sub.example.com/clash/",\n    "tgBotEnable": false,\n    "tgBotToken": "",\n    ...\n  }\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/defaultSettings',
        summary:
          'Return the computed default settings based on the request host. Useful to preview what a fresh install would use.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/factoryDefaults',
        summary:
          'Return the shipped (factory) default value per browser-safe setting key, so clients can tell a stored value apart from the default it would fall back to. Per-install material (secret, panelGuid, mTLS keys) and credential fields are never included.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/update',
        summary:
          'Persist every setting at once. The body mirrors the shape returned by /all. Invalid values (bad ports, missing cert pairs, etc.) are rejected before write.',
        body: '{\n  "webPort": 2053,\n  "webBasePath": "/",\n  "subPort": 10882,\n  "subPath": "/sub/",\n  "subClashAutoDetect": false,\n  "subClashUserAgentRegex": "",\n  "subJsonEnable": false,\n  "subJsonAutoDetect": false,\n  "subJsonAlwaysArray": false,\n  "subJsonUserAgentRegex": "",\n  "subJsonPath": "/json/",\n  "subJsonURI": "https://sub.example.com/json/",\n  "subClashEnable": true,\n  "subClashPath": "/clash/",\n  "subClashURI": "https://sub.example.com/clash/",\n  "tgBotEnable": false,\n  ...\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/ipLimitExempt',
        summary:
          'List the server addresses the IP limit never counts or bans: this panel’s own (name "") and every node’s address plus the address its agent connects from. IPv6 is shown per /64.',
        responseSchema: 'IpLimitExemptHost',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/setting/validateRegex',
        summary:
          'Validate any regular expression with the backend Go RE2 compiler without saving it.',
        body: '{\n  "regex": "(?m)^general-purpose$"\n}',
        response: '{\n  "success": true,\n  "msg": ""\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/updateUser',
        summary:
          'Change the panel admin username and password. Requires the current credentials for verification. The session is refreshed with the new values on success.',
        params: [
          { name: 'oldUsername', in: 'body', type: 'string', desc: 'Current admin username.' },
          { name: 'oldPassword', in: 'body', type: 'string', desc: 'Current admin password.' },
          { name: 'newUsername', in: 'body', type: 'string', desc: 'Desired new username.' },
          { name: 'newPassword', in: 'body', type: 'string', desc: 'Desired new password.' },
        ],
        body: '{\n  "oldUsername": "admin",\n  "oldPassword": "admin",\n  "newUsername": "newadmin",\n  "newPassword": "newpass"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/restartPanel',
        summary:
          'Restart the entire 3x-ui process after a 3-second grace period. The connection drops immediately; the panel comes back online ~5-10 seconds later.',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/testSmtp',
        summary:
          'Test SMTP connection with stage-by-stage reporting (connect, auth, send). Returns structured result with stage and message.',
        response:
          '{\n  "success": true,\n  "stage": "send",\n  "msg": "Test email sent successfully"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/testTgBot',
        summary: 'Test Telegram bot connection by sending a test message to the configured chat.',
        response: '{\n  "success": true,\n  "msg": "Test message sent to Telegram"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/testDiscord',
        summary: 'Test Discord bot connection by sending a test embed to the configured channel.',
        response: '{\n  "success": true,\n  "msg": "Test notification sent successfully"\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/setting/getDefaultJsonConfig',
        summary:
          'Return the built-in default Xray JSON config template that ships with this panel version.',
      },
    ],
  },

  {
    id: 'api-tokens',
    title: 'API Tokens',
    description:
      'Manage scoped Bearer tokens for programmatic auth. Tokens grant admin, monitor, node-sync or ai-usage access, may expire, and are stored as SHA-256 hashes. The plaintext is returned only once at creation.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/setting/apiTokens',
        summary:
          'List every API token, enabled or not. The token value is never returned — only metadata.',
        response:
          '{\n  "success": true,\n  "obj": [\n    {\n      "id": 1,\n      "name": "default",\n      "enabled": true,\n      "createdAt": 1736000000\n    }\n  ]\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/apiTokens/create',
        summary:
          'Mint a scoped API token. The server-generated plaintext is returned only once and stored as a hash.',
        params: [
          {
            name: 'name',
            in: 'body',
            type: 'string',
            desc: 'Human-readable label, e.g. "central-panel-a".',
          },
          {
            name: 'scope',
            in: 'body',
            type: 'string',
            desc: 'admin (default), monitor, node-sync, or ai-usage.',
            optional: true,
          },
          {
            name: 'expiresAt',
            in: 'body',
            type: 'number',
            desc: 'Future Unix milliseconds, or 0 for no expiry.',
            optional: true,
          },
        ],
        body: '{\n  "name": "central-panel-a",\n  "scope": "node-sync",\n  "expiresAt": 1798761600000\n}',
        responseSchema: 'ApiTokenView',
        errorResponse:
          '{\n  "success": false,\n  "msg": "a token with that name already exists"\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/apiTokens/delete/:id',
        summary:
          'Permanently delete a token. Any caller using it stops authenticating immediately.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Token row ID.' },
          {
            name: 'expectedScope',
            in: 'body',
            type: 'string',
            desc: 'Stored scope expected by the operator.',
          },
        ],
        body: '{\n  "expectedScope": "node-sync"\n}',
        response: '{\n  "success": true\n}',
      },
      {
        method: 'POST',
        path: '/panel/api/setting/apiTokens/setEnabled/:id',
        summary:
          'Toggle a token enabled/disabled without deleting it. Disabled tokens are rejected by checkAPIAuth on the next request.',
        params: [
          { name: 'id', in: 'path', type: 'number', desc: 'Token row ID.' },
          { name: 'enabled', in: 'body', type: 'boolean', desc: 'New enabled state.' },
          {
            name: 'expectedScope',
            in: 'body',
            type: 'string',
            desc: 'Stored scope expected by the operator.',
          },
        ],
        body: '{\n  "enabled": false,\n  "expectedScope": "node-sync"\n}',
        response: '{\n  "success": true\n}',
      },
    ],
  },

  {
    id: 'xray-settings',
    title: 'Xray Settings',
    description:
      "Xray configuration template, the running core's state, and geodata files. All endpoints under /panel/api/xray.",
    endpoints: [
      {
        method: 'POST',
        path: '/panel/api/xray/',
        summary:
          'Return the Xray config template and the standard geodata sources in one response.',
        response:
          '{\n  "success": true,\n  "obj": {\n    "xraySetting": "{...raw xray config...}",\n    "geodataSources": [\n      { "url": "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat", "file": "geoip.dat" }\n    ]\n  }\n}',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/getDefaultJsonConfig',
        summary:
          'Return the built-in default Xray config shipped with the panel (identical to /panel/api/setting/getDefaultJsonConfig).',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/getXrayResult',
        summary:
          'Return the most recent Xray process stdout/stderr output. Useful to check for startup errors or runtime warnings.',
      },
      {
        method: 'POST',
        path: '/panel/api/xray/update',
        summary:
          'Save the Xray JSON config template (a form field) and apply it to a running core.',
        params: [
          {
            name: 'xraySetting',
            in: 'body (form)',
            type: 'string',
            desc: 'Full Xray JSON config template.',
          },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/xray/geodata/files',
        summary:
          'List the geo databases (.dat files) in the Xray asset folder, with the layout detected from their contents, size, modification time and category count. A database that fails to parse is still listed, with the reason in "error".',
      },
      {
        method: 'GET',
        path: '/panel/api/xray/geodata/categories',
        summary:
          'One page of a database\'s categories, each with its entry count and the attributes its domains carry (e.g. "ads", "cn").',
        params: [
          {
            name: 'file',
            in: 'query',
            type: 'string',
            desc: 'Database file name inside the asset folder, e.g. geosite.dat (required).',
          },
          {
            name: 'q',
            in: 'query',
            type: 'string',
            optional: true,
            desc: 'Case-insensitive substring filter on the category code.',
          },
          {
            name: 'offset',
            in: 'query',
            type: 'integer',
            optional: true,
            desc: 'Rows to skip. Defaults to 0.',
          },
          {
            name: 'limit',
            in: 'query',
            type: 'integer',
            optional: true,
            desc: 'Rows to return, capped at 500. Omit it to return every category — the index is small and the panel filters it client-side.',
          },
        ],
      },
      {
        method: 'GET',
        path: '/panel/api/xray/geodata/entries',
        summary:
          'One page of the rules inside a category — domain rules typed as domain/full/keyword/regexp for geosite databases, CIDRs for geoip ones.',
        params: [
          {
            name: 'file',
            in: 'query',
            type: 'string',
            desc: 'Database file name inside the asset folder (required).',
          },
          {
            name: 'code',
            in: 'query',
            type: 'string',
            desc: 'Category code, case-insensitive, e.g. google (required).',
          },
          {
            name: 'q',
            in: 'query',
            type: 'string',
            optional: true,
            desc: 'Case-insensitive substring filter on the rule value.',
          },
          {
            name: 'offset',
            in: 'query',
            type: 'integer',
            optional: true,
            desc: 'Rows to skip. Defaults to 0.',
          },
          {
            name: 'limit',
            in: 'query',
            type: 'integer',
            optional: true,
            desc: 'Rows to return, capped at 500. Defaults to the cap.',
          },
        ],
      },
      {
        method: 'POST',
        path: '/panel/api/xray/geodata/validate',
        summary:
          'Check routing tokens against the databases on disk and return only the ones that do not resolve. Plain domains and CIDRs are ignored. Each issue carries a reason: syntax, fileMissing or categoryMissing.',
        params: [
          {
            name: 'tokens',
            in: 'body (form)',
            type: 'string',
            desc: 'Comma-separated routing tokens, e.g. "geosite:google,geosite:blabla". Max 500 per request.',
          },
          {
            name: 'kind',
            in: 'body (form)',
            type: 'string',
            desc: '"ip" to parse the tokens as IP rules (geoip:, ext-ip:, leading !). Anything else parses them as domain rules (geosite:, ext-site:).',
            optional: true,
          },
        ],
        body: 'kind=domain&tokens=geosite:google,geosite:blabla',
      },
    ],
  },

  {
    id: 'sub-balancers',
    title: 'Subscription Balancers',
    description:
      'Client-side balancers for the JSON subscription: each enabled balancer is emitted as one extra config document whose members are the proxy outbounds of the selected inbounds (routing.balancers + burstObservatory). Managed in Settings → Sub Balancers.',
    endpoints: [
      {
        method: 'GET',
        path: '/panel/api/sub-balancers',
        summary: 'List all subscription balancers in sort order (sort_order asc, id asc).',
        responseSchema: 'SubBalancer',
        responseSchemaArray: true,
      },
      {
        method: 'POST',
        path: '/panel/api/sub-balancers',
        summary:
          'Create a subscription balancer. It appears in the JSON subscription of every client that sits on at least one selected inbound.',
        params: subBalancerBodyParams,
        responseSchema: 'SubBalancer',
      },
      {
        method: 'POST',
        path: '/panel/api/sub-balancers/:id',
        summary:
          'Update a balancer by id. Accepts the same form fields as create (full-row update); omitting memberWeights clears stored weights, while omitting enabled keeps its current value.',
        params: [
          { name: 'id', in: 'path', type: 'integer', desc: 'Balancer id.' },
          ...subBalancerBodyParams,
        ],
        responseSchema: 'SubBalancer',
      },
      {
        method: 'DELETE',
        path: '/panel/api/sub-balancers/:id',
        summary: 'Delete a balancer by id.',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Balancer id.' }],
        responseSchema: 'SubBalancer',
      },
      {
        method: 'POST',
        path: '/panel/api/sub-balancers/:id/del',
        summary:
          'Delete a balancer by id (POST alias of DELETE for clients that cannot send DELETE).',
        params: [{ name: 'id', in: 'path', type: 'integer', desc: 'Balancer id.' }],
        responseSchema: 'SubBalancer',
      },
    ],
  },

  {
    id: 'subscription',
    title: 'Subscription Server',
    description:
      'A separate HTTP/HTTPS server that serves proxy subscription links (standard, JSON, and Clash) to clients. The server listens on its own port (default 2096) and is configured in Settings → Subscription. Fresh panels generate random path prefixes for each format; all paths remain configurable. Every subscription endpoint sets response headers for client apps to read traffic/expiry info.',
    subHeader: [
      {
        name: 'Subscription-Userinfo',
        desc: 'Traffic and expiry: <code>upload=N; download=N; total=N; expire=TS</code>',
      },
      { name: 'Profile-Title', desc: 'Base64-encoded subscription display name' },
      { name: 'Profile-Web-Page-Url', desc: 'Link to the subscription info page' },
      { name: 'Support-Url', desc: 'Support contact URL configured in settings' },
      {
        name: 'Profile-Update-Interval',
        desc: 'Suggested polling interval in minutes (e.g. <code>10</code>)',
      },
      { name: 'Announce', desc: 'Base64-encoded announcement string' },
      {
        name: 'Routing-Enable',
        desc: '<code>true</code> or <code>false</code> — whether routing rules are included',
      },
      {
        name: 'Routing',
        desc: 'Global routing rules for client apps that support them (e.g. Happ)',
      },
    ],
    endpoints: [
      {
        method: 'POST',
        path: '/{subPath}portal/register',
        summary:
          'Register a username and password with a single-use activation code and create a portal session. The code supplies the plan, quota, validity and monthly reset day. Failed requests are limited per address.',
        body: '{"username":"new-user","password":"example-password","code":"ABCD-EFGH-JKLM-NPQR"}',
        response: '{"success":true}',
      },
      {
        method: 'POST',
        path: '/{subPath}portal/redeem',
        summary:
          'Redeem an activation code for the signed-in user. Changes the plan, quota and reset day, adds validity from the later of now and expiry, and clears usage. A failed service update can be retried with the same code without granting the days twice.',
        body: '{"code":"ABCD-EFGH-JKLM-NPQR"}',
        response: '{"success":true}',
      },
      {
        method: 'POST',
        path: '/{subPath}portal/admin-login',
        summary:
          'Verify administrator credentials and a current authenticator code. Two-factor authentication must be enabled. Returns a browser-bound single-use handoff to POST to the panel portal-admin endpoint within one minute.',
        body: '{"username":"admin","password":"example-password","twoFactorCode":"123456"}',
        response: '{"success":true,"token":"browser-bound-token","path":"/portal-admin"}',
      },
      {
        method: 'GET',
        path: '/{subPath}:subid/probe',
        summary:
          'Return probe information for only the hosts behind this subscription. Uses the portal probe field whitelist and excludes unrelated hosts. Unknown tokens return 404. Responses must not be cached.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Subscription ID.' }],
        responses: {
          '200': {
            description: 'Probe information scoped to the subscription.',
            content: {
              'application/json': { schema: { $ref: '#/components/schemas/PortalProbe' } },
            },
          },
          '404': { description: 'Unknown subscription token.' },
        },
      },
      {
        method: 'GET',
        path: '/{subPath}:subid',
        summary:
          'Return base64-encoded subscription links for all enabled clients matching the subscription ID. When the request has an Accept: text/html header or ?html=1, renders a styled info page instead. With ?format=info, returns the page view-model as JSON (traffic, expiry, online status; no links) for live polling. The path prefix is configured by subPath.',
        params: [
          { name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' },
          {
            name: 'format',
            in: 'query',
            type: 'string',
            optional: true,
            desc: 'Set to "info" to get the subscription status view-model as JSON instead of the links.',
          },
        ],
      },
      {
        method: 'HEAD',
        path: '/{subPath}:subid',
        summary:
          'Return the same status and subscription metadata headers as GET without a response body.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
        responses: subscriptionHeadResponses,
      },
      {
        method: 'GET',
        path: '/{subPath}:subid/hwid-status',
        summary:
          'Return aggregate HWID device-slot usage for the subscription: whether an HWID limit is active, the limit, how many devices are registered and how many slots remain. Read-only — it never registers a device, so asking does not consume a slot. Counters only: no HWID value, email or device metadata. The path prefix is configured by subPath.',
        description:
          'Responds with the bare HwidSlotStatus object, not the <code>{success,msg,obj}</code> panel envelope, like the other subscription-server routes. With no HWID limit configured, <code>active</code> is false and every counter is 0.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
        responses: {
          '200': {
            description: 'Device-slot counters for the subscription.',
            content: {
              'application/json': { schema: { $ref: '#/components/schemas/HwidSlotStatus' } },
            },
          },
          ...hwidStatusErrorResponses,
        },
      },
      {
        method: 'HEAD',
        path: '/{subPath}:subid/hwid-status',
        summary:
          'Return the HWID device-slot status code and headers as GET without a response body.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
        responses: {
          '200': { description: 'Headers match GET; no response body.' },
          ...hwidStatusErrorResponses,
        },
      },
      {
        method: 'GET',
        path: '/{jsonPath}:subid',
        summary:
          'Return subscription as a JSON array of proxy configs (one per enabled client). Only when JSON subscription is enabled in settings. The path prefix is configured by subJsonPath.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
      },
      {
        method: 'HEAD',
        path: '/{jsonPath}:subid',
        summary:
          'Return the JSON subscription status and metadata headers without a body. Registered only when JSON subscriptions are enabled.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
        responses: subscriptionHeadResponses,
      },
      {
        method: 'GET',
        path: '/{clashPath}:subid',
        summary:
          'Return subscription as a Clash/Mihomo-compatible YAML config, including configured global Clash routing rules. Only when Clash subscription is enabled in settings. The path prefix is configured by subClashPath.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
      },
      {
        method: 'HEAD',
        path: '/{clashPath}:subid',
        summary:
          'Return the Clash subscription status and metadata headers without a body. Registered only when Clash subscriptions are enabled.',
        params: [{ name: 'subid', in: 'path', type: 'string', desc: 'Client subscription ID.' }],
        responses: subscriptionHeadResponses,
      },
    ],
  },

  {
    id: 'websocket',
    title: 'WebSocket',
    description:
      'Real-time status updates via WebSocket. Connect once at <code>ws://<panel>/ws</code> to receive a stream of JSON messages without polling. Requires an authenticated session cookie (Bearer token auth is not supported). Each message has a <code>type</code> field that identifies the payload shape.',
    endpoints: [
      {
        method: 'GET',
        path: '/ws',
        summary:
          'Upgrade an HTTP connection to a WebSocket. Requires an authenticated session cookie (Bearer token auth is not supported here). Returns 101 Switching Protocols on success. The server then pushes JSON messages described below.',
        responses: {
          '101': { description: 'Switching Protocols. WebSocket messages use WebSocketEnvelope.' },
          '401': { description: 'No authenticated panel session cookie.' },
        },
        security: [{ cookieAuth: [] }],
      },
    ],
  },
];
