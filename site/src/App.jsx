import { useEffect, useMemo, useState } from "react";
import {
  FiArrowRight,
  FiBookOpen,
  FiBox,
  FiCheck,
  FiCopy,
  FiExternalLink,
  FiGift,
  FiMenu,
  FiRadio,
  FiRefreshCw,
  FiServer,
  FiShield,
  FiX,
} from "react-icons/fi";
import {
  SiDocker,
  SiGithub,
  SiGo,
  SiKubernetes,
  SiPostgresql,
  SiPrometheus,
  SiUnity,
} from "react-icons/si";

const REPOSITORY = "https://github.com/Ruleshift/server";
const GAMEJAM_API_URL = import.meta.env.VITE_GAMEJAM_API_URL?.replace(/\/$/, "") ?? "";

const examples = {
  sdk: {
    label: "GO SDK",
    language: "go",
    code: `client, err := ruleshift.NewClient(baseURL, developerKey, nil)
module, err := client.CreateRuntimeModule(ctx, "my_game", "My Game")
version, err := client.PublishModuleVersion(ctx, publishRequest)
status, err := client.GetValidationStatus(ctx, module.Key, version.Ref.Version)
room, err := client.CreateRoom(ctx, ruleshift.CreateRoomRequest{
    ModuleID: module.Key,
    PlayerCount: 2,
})`,
  },
  module: {
    label: "MODULE ABI",
    language: "protobuf",
    code: `service ModuleRuntime {
  rpc Describe(DescribeRequest) returns (DescribeResponse);
  rpc CreateState(CreateStateRequest) returns (TransitionResponse);
  rpc Apply(ApplyRequest) returns (TransitionResponse);
  rpc ProjectSnapshot(ProjectRequest) returns (ProjectionResponse);
  rpc ProjectDelta(ProjectDeltaRequest) returns (ProjectionResponse);
}`,
  },
  room: {
    label: "CREATE ROOM",
    language: "powershell",
    code: `$headers = @{ Authorization = "Bearer $env:RULESHIFT_DEVELOPER_API_KEY" }

$room = Invoke-RestMethod -Method Post \
  -Uri "$env:RULESHIFT_URL/v2/rooms" \
  -Headers $headers -ContentType application/json \
  -Body '{"module_id":"mygame","player_count":2}'`,
  },
};

const docs = [
  { title: "Architecture", description: "Room ordering, runtime boundaries, storage and the control plane.", path: "docs/architecture.md", tag: "CORE" },
  { title: "Protocol v2", description: "Binary protobuf envelopes, revisions, reconnect and snapshots.", path: "docs/protocol.md", tag: "PROTOCOL" },
  { title: "Module development", description: "Build a stateless gRPC game module and publish it as an OCI image.", path: "docs/module-development.md", tag: "GUIDE" },
  { title: "Developer API", description: "Publish module versions, validate them and create rooms from trusted code.", path: "docs/developer-api.md", tag: "API" },
  { title: "Unity client", description: "Connect a Unity player build to the protobuf WebSocket endpoint.", path: "docs/unity-client.md", tag: "SDK" },
  { title: "Observability", description: "Prometheus metrics, structured logs, Grafana and safe public diagnostics.", path: "docs/observability.md", tag: "OPS" },
  { title: "Game jam promotions", description: "Discover, moderate and verify promotion codes for Russian game jams.", path: "docs/gamejam-promotions.md", tag: "PROMO" },
  { title: "Database", description: "Control data, room persistence, snapshots and isolated module storage.", path: "docs/database.md", tag: "STORAGE" },
  { title: "Steam integration", description: "Replaceable authentication and Steam ticket verification boundaries.", path: "docs/steam-integration.md", tag: "AUTH" },
  { title: "Performance report", description: "Benchmark scope, hot paths and the measurements that matter.", path: "docs/performance-report.md", tag: "PERF" },
];

function getPage() {
  return new URLSearchParams(window.location.search).get("page") === "docs" ? "docs" : "home";
}

function Brand() {
  return (
    <img src="/rs_logo_trans.png" className="brand-logo" alt="Ruleshift" />
  );
}

function Header({ page, navigate }) {
  const [open, setOpen] = useState(false);

  useEffect(() => setOpen(false), [page]);

  const scrollTo = (id) => {
    if (page !== "home") {
      navigate("home", id);
      return;
    }
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    setOpen(false);
  };

  return (
    <header className="site-header">
      <button className="brand-button" onClick={() => navigate("home")}><Brand /></button>
      <button className="menu-button" onClick={() => setOpen((value) => !value)} aria-expanded={open} aria-label="Toggle navigation">
        {open ? <FiX /> : <FiMenu />}
      </button>
      <nav className={open ? "main-nav is-open" : "main-nav"} aria-label="Main navigation">
        <button className={page === "home" ? "active" : ""} onClick={() => navigate("home")}>OVERVIEW</button>
        <button onClick={() => scrollTo("architecture")}>ARCHITECTURE</button>
        <button onClick={() => scrollTo("examples")}>EXAMPLES</button>
        <button className={page === "docs" ? "active" : ""} onClick={() => navigate("docs")}>DOCS</button>
      </nav>
      <a className="github-link" href={REPOSITORY} target="_blank" rel="noreferrer">
        <SiGithub aria-hidden="true" /> GITHUB <FiExternalLink aria-hidden="true" />
      </a>
    </header>
  );
}

function Sticker({ className, icon: Icon, label }) {
  return (
    <span className={`tech-sticker ${className}`} aria-label={label} title={label}>
      <Icon />
    </span>
  );
}

function Hero({ navigate }) {
  return (
    <section className="hero" aria-labelledby="hero-title">
      <Sticker className="sticker-go" icon={SiGo} label="Go" />
      <Sticker className="sticker-k8s" icon={SiKubernetes} label="Kubernetes" />
      <Sticker className="sticker-pg" icon={SiPostgresql} label="PostgreSQL" />
      <Sticker className="sticker-docker" icon={SiDocker} label="Docker" />
      <Sticker className="sticker-unity" icon={SiUnity} label="Unity" />
      <Sticker className="sticker-prom" icon={SiPrometheus} label="Prometheus" />

      <p className="eyebrow"><span className="status-dot" /> AUTHORITATIVE MULTIPLAYER STATE</p>
      <h1 id="hero-title">ONE ROOM.<br />ONE ORDER.<br /><span>ZERO DRIFT.</span></h1>
      <p className="hero-copy">
        Ruleshift orders protobuf commands, runs your stateless game module,
        persists the result and broadcasts one coherent revision stream.
      </p>
      <div className="hero-actions">
        <button className="button button-primary stack-button" onClick={() => document.getElementById("architecture")?.scrollIntoView({ behavior: "smooth" })}>
          EXPLORE THE SYSTEM <FiArrowRight />
        </button>
        <button className="button button-secondary" onClick={() => navigate("docs")}>
          READ THE DOCS
        </button>
      </div>
      <div className="signal-grid" aria-label="Project principles">
        <div><strong>V2</strong><span>PROTOBUF PROTOCOL</span></div>
        <div><strong>1</strong><span>REVISION STREAM</span></div>
        <div><strong>0</strong><span>UNBOUNDED QUEUES</span></div>
        <div><strong>OCI</strong><span>EXTERNAL MODULES</span></div>
      </div>
    </section>
  );
}

function RuntimePanel() {
  return (
    <section className="runtime-wrap" aria-label="Ruleshift runtime overview">
      <div className="window-shell">
        <div className="window-bar">
          <span className="window-lights"><i /><i /><i /></span>
          <span>ruleshift.dev / room-runtime</span>
          <span className="online"><span /> LIVE</span>
        </div>
        <div className="runtime-grid">
          <div className="runtime-cell terminal-cell">
            <p className="cell-title"><span className="cyan-square" /> COMMAND STREAM</p>
            <code><em>$</em> player.p7 → room.alpha</code>
            <code>command_type <b>increment.v1</b></code>
            <code>expected_revision <b>1041</b></code>
            <code className="success">accepted → queue[07]</code>
          </div>
          <div className="runtime-cell">
            <p className="cell-title"><span className="violet-square" /> REVISION LOG</p>
            <div className="revision-row"><span>rev_1040</span><b>SNAPSHOT</b><time>10:42:08.014</time></div>
            <div className="revision-row is-current"><span>rev_1041</span><b>DELTA</b><time>10:42:08.026</time></div>
            <div className="revision-row"><span>rev_1042</span><b>WAITING</b><time>—</time></div>
          </div>
          <div className="runtime-cell">
            <p className="cell-title"><span className="yellow-square" /> MODULE RUNTIME</p>
            <pre>{`Apply {
  state: opaque_bytes,
  command: protobuf.Any,
  actor: { player_id: "p7", seat_index: 1 }
}`}</pre>
          </div>
          <div className="runtime-cell">
            <p className="cell-title"><span className="red-square" /> INVARIANTS</p>
            <ul className="check-list">
              <li><FiCheck /> sequential apply</li>
              <li><FiCheck /> transactional persist</li>
              <li><FiCheck /> projected broadcast</li>
              <li><FiCheck /> snapshot recovery</li>
            </ul>
          </div>
        </div>
      </div>
    </section>
  );
}

const features = [
  { icon: FiRadio, title: "COHERENT BY DEFAULT", copy: "Every client in a room observes the same ordered stream of authoritative revisions." },
  { icon: FiServer, title: "SEQUENTIAL RUNTIMES", copy: "Bounded command queues keep room logic predictable and independent from network writes." },
  { icon: FiBox, title: "GAME-AGNOSTIC CORE", copy: "Ship new game rules as stateless gRPC/protobuf OCI images without rebuilding Ruleshift." },
  { icon: FiRefreshCw, title: "RECOVERABLE SESSIONS", copy: "Reconnect through snapshots and replay against the exact module version pinned to the room." },
  { icon: FiShield, title: "HARD TENANT BOUNDARIES", copy: "Per-tenant namespaces, quotas, default-deny networking and non-root module containers." },
  { icon: SiPrometheus, title: "VISIBLE HOT PATHS", copy: "Prometheus metrics, structured logs, dashboards and payload-free operational projections." },
];

function FeatureSection() {
  return (
    <section className="section feature-section" id="features">
      <div className="section-kicker">WHAT THE CORE GUARANTEES</div>
      <div className="section-heading-row">
        <h2>SHIP GAME RULES.<br /><span>KEEP STATE BORING.</span></h2>
        <p>A small, explicit state service for teams that would rather build gameplay than debug distributed ordering.</p>
      </div>
      <div className="feature-grid">
        {features.map(({ icon: Icon, title, copy }, index) => (
          <article className="feature-card" key={title}>
            <span className={`feature-icon accent-${index % 4}`}><Icon /></span>
            <span className="feature-index">0{index + 1}</span>
            <h3>{title}</h3>
            <p>{copy}</p>
          </article>
        ))}
      </div>
    </section>
  );
}

const flow = [
  { no: "01", title: "CLIENT COMMAND", copy: "An authenticated player sends a protobuf command with the expected room revision." },
  { no: "02", title: "ORDER + APPLY", copy: "The bounded room runtime invokes the exact pinned stateless module version." },
  { no: "03", title: "PERSIST", copy: "State, event and snapshot data commit before the room revision becomes visible." },
  { no: "04", title: "PROJECT + BROADCAST", copy: "Each recipient receives the correct public, private or full protobuf projection." },
];

function ArchitectureSection() {
  return (
    <section className="section architecture-section" id="architecture">
      <div className="section-kicker light">THE AUTHORITATIVE LOOP</div>
      <div className="section-heading-row light">
        <h2>FROM COMMAND TO<br /><span>COHERENT REVISION.</span></h2>
        <p>The network can reconnect. Modules can restart. The room’s order remains the source of truth.</p>
      </div>
      <div className="flow-list">
        {flow.map((item, index) => (
          <article className="flow-step" key={item.no}>
            <span className="flow-number">{item.no}</span>
            <div><h3>{item.title}</h3><p>{item.copy}</p></div>
            {index < flow.length - 1 && <FiArrowRight className="flow-arrow" aria-hidden="true" />}
          </article>
        ))}
      </div>
      <div className="boundary-note">
        <span><FiShield /> TRUST BOUNDARY</span>
        <p>Clients send intent, never state. Developer keys stay in Editor tooling, CI or a trusted backend.</p>
      </div>
    </section>
  );
}

function CodeExample() {
  const [tab, setTab] = useState("sdk");
  const [copied, setCopied] = useState(false);
  const selected = examples[tab];

  const copy = async () => {
    await navigator.clipboard.writeText(selected.code);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  };

  return (
    <section className="section examples-section" id="examples">
      <div className="section-kicker">BUILD ON THE CONTRACT</div>
      <div className="section-heading-row">
        <h2>YOUR GAME.<br /><span>YOUR TYPES.</span></h2>
        <p>Ruleshift owns ordering and persistence. Your repository owns game state, commands and projections.</p>
      </div>
      <div className="code-window">
        <div className="code-tabs" role="tablist" aria-label="Code examples">
          {Object.entries(examples).map(([key, value]) => (
            <button key={key} className={tab === key ? "active" : ""} onClick={() => { setTab(key); setCopied(false); }} role="tab" aria-selected={tab === key}>
              {value.label}
            </button>
          ))}
          <button className="copy-button" onClick={copy}>{copied ? <FiCheck /> : <FiCopy />} {copied ? "COPIED" : "COPY"}</button>
        </div>
        <div className="code-meta"><span>{selected.language}</span><span>UTF-8</span></div>
        <pre className="code-body"><code>{selected.code}</code></pre>
      </div>
      <div className="example-links">
        <a href={`${REPOSITORY}/tree/main/examples/modules/hiddennumber`} target="_blank" rel="noreferrer">HIDDEN NUMBER <FiArrowRight /></a>
        <a href={`${REPOSITORY}/tree/main/examples/modules/xiangqi`} target="_blank" rel="noreferrer">XIANGQI <FiArrowRight /></a>
        <a href={`${REPOSITORY}/tree/main/examples/modules/cardgame`} target="_blank" rel="noreferrer">CARD GAME <FiArrowRight /></a>
      </div>
    </section>
  );
}

function GameJamDiscount() {
  const [code, setCode] = useState("");
  const [state, setState] = useState("idle");
  const [gameJam, setGameJam] = useState(null);

  const submit = async (event) => {
    event.preventDefault();
    if (code.length !== 10 || state === "loading") return;
    setState("loading");
    setGameJam(null);
    try {
      const response = await fetch(`${GAMEJAM_API_URL}/v1/gamejam-discounts/verify`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
        credentials: "omit",
      });
      if (!response.ok) throw new Error("verification unavailable");
      const result = await response.json();
      if (!result.valid) {
        setState("invalid");
        return;
      }
      if (!result.game_jam || typeof result.game_jam.title !== "string" || !result.game_jam.title) {
        throw new Error("invalid verification response");
      }
      setGameJam(result.game_jam);
      setState("success");
    } catch {
      setState("error");
    }
  };

  const resultMessage = state === "success"
    ? `Код подтверждён. Для участников «${gameJam?.title}» доступна скидка Ruleshift. Размер и условия будут объявлены позднее.`
    : state === "invalid"
      ? "Код недействителен или сейчас не действует."
      : state === "error"
        ? "Не удалось проверить код. Попробуйте ещё раз позже."
        : "";

  return (
    <section className="gamejam-discount" aria-labelledby="gamejam-discount-title">
      <div className="discount-copy">
        <p className="section-kicker"><FiGift /> GAME JAM DISCOUNT</p>
        <h2 id="gamejam-discount-title">JAM TOGETHER.<br /><span>SHIP WITH RULESHIFT.</span></h2>
        <p>Участвуете в российском или русскоязычном game jam? Введите код от организатора, чтобы подтвердить право на будущую скидку Ruleshift.</p>
      </div>
      <form className="discount-form" onSubmit={submit}>
        <label htmlFor="gamejam-code">10-DIGIT JAM CODE</label>
        <div className="discount-input-row">
          <input
            id="gamejam-code"
            value={code}
            onChange={(event) => {
              setCode(event.target.value.replace(/\D/g, "").slice(0, 10));
              setState("idle");
              setGameJam(null);
            }}
            inputMode="numeric"
            autoComplete="off"
            maxLength={10}
            placeholder="0000000000"
            aria-describedby="gamejam-code-hint gamejam-code-result"
          />
          <button type="submit" disabled={code.length !== 10 || state === "loading"}>
            {state === "loading" ? "CHECKING…" : "VERIFY CODE"} <FiArrowRight />
          </button>
        </div>
        <span id="gamejam-code-hint" className="discount-hint">Код не сохраняется на этом устройстве.</span>
        <div id="gamejam-code-result" className={`discount-result ${state}`} aria-live="polite">
          {resultMessage}
        </div>
      </form>
    </section>
  );
}

function HomePage({ navigate }) {
  return (
    <main>
      <Hero navigate={navigate} />
      <RuntimePanel />
      <FeatureSection />
      <ArchitectureSection />
      <CodeExample />
      <GameJamDiscount />
      <section className="cta-section">
        <div>
          <p className="section-kicker light">PROTOBUF IN. COHERENCE OUT.</p>
          <h2>BUILD THE GAME.<br /><span>RULESHIFT THE STATE.</span></h2>
        </div>
        <div className="cta-actions">
          <button className="button button-primary stack-button" onClick={() => navigate("docs")}>OPEN DOCUMENTATION <FiArrowRight /></button>
          <a className="button button-secondary" href={REPOSITORY} target="_blank" rel="noreferrer">VIEW SOURCE <FiExternalLink /></a>
        </div>
      </section>
    </main>
  );
}

function DocsPage() {
  const [query, setQuery] = useState("");
  const filteredDocs = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return docs;
    return docs.filter((item) => `${item.title} ${item.description} ${item.tag}`.toLowerCase().includes(normalized));
  }, [query]);

  return (
    <main className="docs-page">
      <section className="docs-hero">
        <p className="eyebrow"><FiBookOpen /> DOCUMENTATION INDEX</p>
        <h1>START WITH THE<br /><span>RIGHT CONTRACT.</span></h1>
        <p>Architecture, protobuf protocols, module development and production operations — mapped to the source repository.</p>
        <label className="docs-search">
          <span>SEARCH DOCS</span>
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="protocol, modules, Unity…" />
          <kbd>/</kbd>
        </label>
      </section>

      <section className="docs-layout">
        <aside className="docs-aside">
          <p>QUICK START</p>
          <ol>
            <li><span>01</span> Read the architecture</li>
            <li><span>02</span> Choose a module example</li>
            <li><span>03</span> Generate protobuf bindings</li>
            <li><span>04</span> Run the Go test suite</li>
          </ol>
          <div className="aside-command"><code>go test ./...</code></div>
        </aside>
        <div className="docs-content">
          <div className="docs-result-bar"><span>{String(filteredDocs.length).padStart(2, "0")} DOCUMENTS</span><span>MAIN BRANCH</span></div>
          <div className="docs-grid">
            {filteredDocs.map((item, index) => (
              <a className="doc-card" href={`${REPOSITORY}/blob/main/${item.path}`} target="_blank" rel="noreferrer" key={item.path}>
                <div><span className="doc-tag">{item.tag}</span><span className="doc-number">{String(index + 1).padStart(2, "0")}</span></div>
                <h2>{item.title}</h2>
                <p>{item.description}</p>
                <span className="doc-link">READ ON GITHUB <FiExternalLink /></span>
              </a>
            ))}
          </div>
          {filteredDocs.length === 0 && <div className="empty-state"><strong>NO MATCHING DOCUMENTS.</strong><span>Try a broader systems term.</span></div>}
        </div>
      </section>
    </main>
  );
}

function Footer({ navigate }) {
  return (
    <footer className="site-footer">
      <div className="footer-brand"><Brand /><p>Authoritative multiplayer state for game developers.</p></div>
      <div><p className="footer-title">PROJECT</p><button onClick={() => navigate("home")}>Overview</button><button onClick={() => navigate("docs")}>Documentation</button><a href={`${REPOSITORY}/tree/main/examples/modules`}>Examples</a></div>
      <div><p className="footer-title">SOURCE</p><a href={REPOSITORY}>GitHub</a><a href={`${REPOSITORY}/blob/main/go.mod`}>Go module</a><a href={`${REPOSITORY}/actions`}>Build status</a></div>
      <div className="footer-meta"><span>GO + PROTOBUF</span><span>© 2026 RULESHIFT</span></div>
    </footer>
  );
}

export function App() {
  const [page, setPage] = useState(getPage);

  useEffect(() => {
    const onPopState = () => setPage(getPage());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = (nextPage, anchor) => {
    const url = nextPage === "docs" ? "?page=docs" : "./";
    window.history.pushState({}, "", url);
    setPage(nextPage);
    window.scrollTo({ top: 0, behavior: "instant" });
    if (anchor) window.setTimeout(() => document.getElementById(anchor)?.scrollIntoView({ behavior: "smooth" }), 0);
  };

  return (
    <div className="app-shell">
      <Header page={page} navigate={navigate} />
      {page === "docs" ? <DocsPage /> : <HomePage navigate={navigate} />}
      <Footer navigate={navigate} />
    </div>
  );
}
