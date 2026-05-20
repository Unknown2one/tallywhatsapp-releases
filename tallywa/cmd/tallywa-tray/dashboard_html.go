package main

// dashboardHTML is the single-file dashboard the tray opens in the
// user's browser. Tailwind-free; everything is inline so the page
// works offline and after we sign the service binary.
//
// Design notes:
//   - Polls /status every 2s — cheap because it reads the cached
//     snapshot, not the service.
//   - All mutating calls go through /proxy/* which signs HMAC server-
//     side. The page never sees the secret.
//   - QR rendering uses the goqrcode-style data URL the service
//     returns; we embed it as <img> rather than re-rendering JS-side.
//   - Activity log polls /proxy/api/queue/list — same auth path as
//     every other action, no extra surface.
const dashboardHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>TallyWhatsApp</title>
<style>
  :root {
    --bg: #f5f6f8;
    --bg-soft: #ffffff;
    --ink-900: #0b0f1a;
    --ink-700: #2b3340;
    --ink-500: #5a6473;
    --ink-400: #7c8694;
    --ink-300: #aab2bd;
    --line: #e3e6eb;
    --line-strong: #cdd2da;
    --green-50: #e6f9ef;
    --green-100: #c5f0d8;
    --green-500: #25d366;
    --green-600: #1bb554;
    --green-700: #128a3f;
    --amber-50: #fff6e0;
    --amber-500: #f5a623;
    --amber-700: #a86f10;
    --red-50: #ffeaea;
    --red-500: #e23a3a;
    --red-700: #a52121;
    --blue-50: #e8f1ff;
    --blue-500: #2f6ee0;
    --shadow-sm: 0 1px 2px rgba(15,23,42,.04), 0 1px 3px rgba(15,23,42,.06);
    --shadow-md: 0 4px 12px rgba(15,23,42,.06), 0 2px 4px rgba(15,23,42,.04);
    --radius: 12px;
    --radius-sm: 8px;
  }
  * { box-sizing: border-box; }
  html, body { background: var(--bg); }
  body {
    font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
    color: var(--ink-900);
    margin: 0;
    -webkit-font-smoothing: antialiased;
  }
  .wrap { max-width: 980px; margin: 0 auto; padding: 28px 24px 64px; }

  /* Header */
  .topbar { display:flex; align-items:center; justify-content:space-between; margin-bottom: 24px; }
  .brand { display:flex; align-items:center; gap:10px; }
  .brand-mark {
    width: 32px; height: 32px; border-radius: 8px;
    background: linear-gradient(135deg, var(--green-500), var(--green-700));
    color: #fff; display:grid; place-items:center; font-weight:700; font-size:15px;
    box-shadow: var(--shadow-sm);
  }
  .brand-name { font-weight: 700; font-size: 16px; letter-spacing: -.01em; }
  .brand-ver { color: var(--ink-400); font-size: 12px; margin-left: 4px; font-weight: 500; }

  /* Top status pill */
  .status-pill {
    display:inline-flex; align-items:center; gap:8px;
    padding: 6px 12px; border-radius: 999px;
    background: #fff; border: 1px solid var(--line);
    font-weight: 500; font-size: 12.5px;
    box-shadow: var(--shadow-sm);
  }
  .status-pill .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--ink-300); }
  .status-pill.ok { color: var(--green-700); border-color: var(--green-100); background: var(--green-50); }
  .status-pill.ok .dot { background: var(--green-500); box-shadow: 0 0 0 3px rgba(37,211,102,.18); }
  .status-pill.warn { color: var(--amber-700); border-color: #f3dfa6; background: var(--amber-50); }
  .status-pill.warn .dot { background: var(--amber-500); }
  .status-pill.err { color: var(--red-700); border-color: #f3c0c0; background: var(--red-50); }
  .status-pill.err .dot { background: var(--red-500); }

  /* Cards */
  .card {
    background: var(--bg-soft);
    border: 1px solid var(--line);
    border-radius: var(--radius);
    box-shadow: var(--shadow-sm);
    padding: 20px;
    margin-bottom: 16px;
  }
  .card.tight { padding: 0; overflow: hidden; }
  .card-head {
    display:flex; align-items:center; justify-content:space-between;
    padding: 16px 20px; border-bottom: 1px solid var(--line);
  }
  .card-title { font-size: 13px; font-weight: 600; letter-spacing: .02em; text-transform: uppercase; color: var(--ink-500); }

  /* Grid for KPI strip */
  .kpis { display:grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 20px; }
  .kpi {
    background: var(--bg-soft); border: 1px solid var(--line);
    border-radius: var(--radius); padding: 14px 16px;
    box-shadow: var(--shadow-sm);
  }
  .kpi .label { font-size: 11.5px; color: var(--ink-500); text-transform: uppercase; letter-spacing: .04em; font-weight: 600; }
  .kpi .value { font-size: 22px; font-weight: 700; margin-top: 4px; letter-spacing: -.01em; }
  .kpi.green .value { color: var(--green-700); }
  .kpi.amber .value { color: var(--amber-700); }
  .kpi.red .value { color: var(--red-700); }
  .kpi.muted .value { color: var(--ink-700); }
  .kpi-sub { font-size: 11.5px; color: var(--ink-400); margin-top: 2px; }

  /* Two-column section row */
  .grid-2 { display:grid; grid-template-columns: 1.2fr .8fr; gap: 16px; align-items: start; }
  @media (max-width: 820px) { .grid-2 { grid-template-columns: 1fr; } .kpis { grid-template-columns: repeat(2,1fr);} }

  /* Buttons / inputs */
  button {
    font: inherit;
    background: var(--green-500); color: #04130a; border: 0;
    padding: 9px 14px; border-radius: var(--radius-sm); font-weight: 600;
    cursor: pointer; transition: filter .12s, transform .04s;
  }
  button:hover { filter: brightness(1.05); }
  button:active { transform: translateY(1px); }
  button.secondary { background: #eef0f3; color: var(--ink-700); }
  button.secondary:hover { background: #e3e7ec; filter: none; }
  button.ghost { background: transparent; color: var(--ink-700); }
  button.ghost:hover { background: #f0f2f5; filter: none; }
  button.danger { background: var(--red-50); color: var(--red-700); }
  button.danger:hover { background: #fcd7d7; filter: none; }
  button.tiny { padding: 5px 10px; font-size: 12px; border-radius: 6px; }
  button:disabled { opacity: .5; cursor: not-allowed; }

  input[type="text"] {
    font: inherit; color: var(--ink-900);
    background: #fff; border: 1px solid var(--line-strong);
    border-radius: var(--radius-sm); padding: 9px 12px; width: 100%;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  input[type="text"]:focus { outline: none; border-color: var(--green-500); box-shadow: 0 0 0 3px rgba(37,211,102,.15); }

  .label { color: var(--ink-500); font-size: 11.5px; text-transform: uppercase; letter-spacing: .04em; font-weight: 600; }

  /* Service / WhatsApp / License rows */
  .row { display:flex; align-items:center; justify-content:space-between; gap: 12px; }
  .row .h { font-weight: 600; font-size: 15px; color: var(--ink-900); }
  .row .s { color: var(--ink-500); font-size: 13px; margin-top: 2px; }

  /* Badges */
  .badge {
    display: inline-flex; align-items: center; gap: 6px;
    padding: 3px 9px; border-radius: 999px; font-size: 11.5px; font-weight: 600;
    border: 1px solid transparent;
  }
  .badge .dot { width: 6px; height: 6px; border-radius: 50%; }
  .badge.ok    { background: var(--green-50); color: var(--green-700); border-color: var(--green-100); }
  .badge.ok    .dot { background: var(--green-500); }
  .badge.warn  { background: var(--amber-50); color: var(--amber-700); border-color: #f3dfa6; }
  .badge.warn  .dot { background: var(--amber-500); }
  .badge.err   { background: var(--red-50);   color: var(--red-700);   border-color: #f3c0c0; }
  .badge.err   .dot { background: var(--red-500); }
  .badge.muted { background: #f0f2f5; color: var(--ink-500); border-color: var(--line); }
  .badge.muted .dot { background: var(--ink-300); }
  .badge.blue  { background: var(--blue-50); color: var(--blue-500); border-color: #c8defb; }
  .badge.blue  .dot { background: var(--blue-500); }

  /* QR */
  .qr-box {
    display: flex; gap: 16px; align-items: flex-start;
  }
  .qr-tile {
    background: #fff; border: 1px solid var(--line); border-radius: var(--radius);
    padding: 12px; display:flex; flex-direction:column; align-items:center; gap: 8px;
    width: 244px;
  }
  .qr-tile img { width: 220px; height: 220px; image-rendering: pixelated; display:block; }
  .qr-tile img:not([src]), .qr-tile img[src=""] { display: none; }
  .qr-tile .small { font-size: 11.5px; color: var(--ink-500); }
  /* Loading skeleton — shown while whatsmeow opens the QR channel.
     Replaced by the QR <img> as soon as the first code arrives. */
  .qr-skeleton {
    width: 220px; height: 220px;
    border-radius: 10px;
    background:
      linear-gradient(135deg, transparent 24%, rgba(37,211,102,.06) 25%, rgba(37,211,102,.06) 75%, transparent 76%),
      linear-gradient(45deg,  transparent 24%, rgba(37,211,102,.06) 25%, rgba(37,211,102,.06) 75%, transparent 76%),
      #f7f8fa;
    background-size: 22px 22px;
    display: flex; align-items: center; justify-content: center;
    flex-direction: column; gap: 12px;
    color: var(--ink-500);
  }
  .qr-skeleton.hidden { display: none; }
  .qr-spinner {
    width: 36px; height: 36px;
    border-radius: 50%;
    border: 3px solid rgba(37,211,102,.18);
    border-top-color: var(--green-500, #25d366);
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .qr-skeleton .lbl { font-size: 12px; font-weight: 500; }
  .help ol { padding-left: 20px; color: var(--ink-700); margin: 6px 0 12px; }
  .help li { margin: 4px 0; }
  .help .privnote { color: var(--ink-500); font-size: 12.5px; }

  /* Reset-QR fallback block — surfaces when the skeleton is stuck */
  .reset-hint {
    margin-top: 14px; padding: 12px 14px;
    background: var(--amber-50); border: 1px solid #f3dfa6;
    border-radius: var(--radius-sm);
  }
  .reset-hint .reset-row {
    display: flex; align-items: center; justify-content: space-between;
    gap: 12px; flex-wrap: wrap;
  }
  .reset-hint details { margin-top: 10px; }
  .reset-hint .cmd-block { margin-top: 8px; }
  .reset-hint .cmd-label {
    font-size: 11px; font-weight: 600; color: var(--ink-500);
    text-transform: uppercase; letter-spacing: .05em;
    margin: 8px 0 4px;
  }
  .reset-hint .cmd-row {
    display: flex; gap: 8px; align-items: stretch;
  }
  .reset-hint .cmd-row pre {
    flex: 1; margin: 0; font-size: 12px;
    user-select: all;
  }
  .reset-hint .cmd-row button { flex: 0 0 auto; align-self: stretch; }

  /* Activity log */
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { text-align: left; font-weight: 600; color: var(--ink-500);
       background: #fafbfc; border-bottom: 1px solid var(--line);
       padding: 10px 14px; font-size: 11.5px; text-transform: uppercase; letter-spacing: .03em; position: sticky; top: 0; }
  td { padding: 12px 14px; border-bottom: 1px solid var(--line); vertical-align: middle; }
  tr:last-child td { border-bottom: 0; }
  tr.row-hover:hover td { background: #fafbfc; }
  td.recipient { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--ink-700); }
  td.when { color: var(--ink-500); white-space: nowrap; }
  td.actions { text-align: right; white-space: nowrap; }
  td.error { color: var(--red-700); font-size: 12px; }
  .empty { padding: 32px 16px; text-align: center; color: var(--ink-400); font-size: 13px; }

  /* Filter bar */
  .filterbar { display:flex; gap: 6px; align-items:center; padding: 12px 16px; border-bottom: 1px solid var(--line); flex-wrap: wrap; }
  .chip {
    background: #fff; border: 1px solid var(--line); color: var(--ink-700);
    padding: 5px 10px; border-radius: 999px; font-size: 12.5px; cursor: pointer; font-weight: 500;
  }
  .chip:hover { background: #f5f7fa; }
  .chip.active { background: var(--ink-900); color: #fff; border-color: var(--ink-900); }
  .filterbar .spacer { flex: 1; }
  .filterbar .meta { color: var(--ink-400); font-size: 12px; }

  /* Modal-y notice for receipt cooldown */
  .notice {
    margin: 12px 0 0; padding: 10px 14px; border-radius: var(--radius-sm);
    background: var(--blue-50); color: var(--blue-500); font-size: 12.5px;
    border: 1px solid #c8defb;
  }

  /* Toast */
  .toast {
    position: fixed; bottom: 24px; left: 50%; transform: translateX(-50%);
    background: var(--ink-900); color: #fff; border-radius: var(--radius-sm);
    padding: 10px 16px; font-size: 13px; font-weight: 500;
    opacity: 0; transition: opacity .2s, transform .2s;
    pointer-events: none; box-shadow: var(--shadow-md);
  }
  .toast.show { opacity: 1; transform: translateX(-50%) translateY(-6px); }

  .footer { margin-top: 28px; color: var(--ink-400); font-size: 12px; text-align: center; }
  details { margin-top: 12px; }
  summary { cursor: pointer; color: var(--ink-500); user-select: none; }
  summary:hover { color: var(--ink-700); }
  pre { font-size: 12px; color: var(--ink-700); background: #f7f8fa; padding: 12px; border-radius: var(--radius-sm); overflow: auto; border: 1px solid var(--line); }

  .err-line { color: var(--red-700); font-size: 13px; margin-top: 8px; }
  .hint { color: var(--ink-500); font-size: 12.5px; }
  .kv { display:grid; grid-template-columns: 110px 1fr; gap: 6px 16px; margin-top: 8px; }
  .kv .v { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
</style>
</head>
<body>
<div class="wrap">

  <div class="topbar">
    <div class="brand">
      <div class="brand-mark">T</div>
      <div>
        <div class="brand-name">TallyWhatsApp <span class="brand-ver" id="brandVer"></span></div>
        <div class="hint" id="subhead">Talking to local service…</div>
      </div>
    </div>
    <div id="topBadge" class="status-pill"><span class="dot"></span><span>connecting…</span></div>
  </div>

  <!-- KPI strip -->
  <div class="kpis">
    <div class="kpi muted"><div class="label">Pending</div><div class="value" id="kpiPending">0</div><div class="kpi-sub">in queue</div></div>
    <div class="kpi green"><div class="label">Sent</div><div class="value" id="kpiSent">0</div><div class="kpi-sub">delivered</div></div>
    <div class="kpi amber"><div class="label">Sending</div><div class="value" id="kpiSending">0</div><div class="kpi-sub">in flight</div></div>
    <div class="kpi red"><div class="label">Failed</div><div class="value" id="kpiDead">0</div><div class="kpi-sub">give-up</div></div>
  </div>

  <!-- Side-by-side: WhatsApp + License -->
  <div class="grid-2">
    <div class="card" id="whatsappCard">
      <div class="row">
        <div>
          <div class="label">WhatsApp</div>
          <div class="h" id="waLabel" style="margin-top:4px;">Not connected</div>
          <div class="s" id="waPhone" hidden>—</div>
        </div>
        <span class="badge muted" id="waBadge"><span class="dot"></span><span>—</span></span>
      </div>

      <div id="qrPanel" hidden style="margin-top:16px;">
        <div class="qr-box">
          <div class="qr-tile">
            <div id="qrSkeleton" class="qr-skeleton">
              <div class="qr-spinner" aria-hidden="true"></div>
              <div class="lbl">Preparing QR…</div>
            </div>
            <img id="qrImg" alt="" hidden>
            <div class="small">Scan with your phone</div>
          </div>
          <div class="help" style="flex:1;">
            <div class="label">How to scan</div>
            <ol>
              <li>Open WhatsApp on your phone.</li>
              <li>Tap <strong>Settings · Linked Devices · Link a Device</strong>.</li>
              <li>Point your phone at the QR on the left.</li>
            </ol>
            <div class="privnote">QR refreshes automatically. Messages stay on this PC — no cloud relay.</div>
          </div>
        </div>

        <div id="resetQrHint" class="reset-hint" hidden>
          <div class="reset-row">
            <div>
              <div class="label">QR not appearing?</div>
              <div class="hint">Restart the connector service. Your messages, license and pairings are unaffected.</div>
            </div>
            <button class="secondary" id="resetQrBtn" type="button">Reset QR</button>
          </div>
          <details>
            <summary>Or run it yourself as Administrator</summary>
            <div class="cmd-block">
              <div class="cmd-label">PowerShell</div>
              <div class="cmd-row">
                <pre id="psResetCmd">Restart-Service TallyWhatsAppConnector</pre>
                <button class="ghost tiny" type="button" data-copy="psResetCmd">Copy</button>
              </div>
              <div class="cmd-label">Command Prompt</div>
              <div class="cmd-row">
                <pre id="cmdResetCmd">net stop TallyWhatsAppConnector &amp;&amp; net start TallyWhatsAppConnector</pre>
                <button class="ghost tiny" type="button" data-copy="cmdResetCmd">Copy</button>
              </div>
            </div>
          </details>
        </div>
      </div>

      <div id="connectedPanel" hidden style="margin-top:14px;">
        <button class="secondary" id="logoutBtn">Log out of WhatsApp</button>
        <button id="reconnectBtn" hidden>Reconnect</button>
      </div>
    </div>

    <div class="card" id="licenseCard">
      <div class="row">
        <div>
          <div class="label">License</div>
          <div class="h" id="licenseLabel" style="margin-top:4px;">Not activated</div>
        </div>
        <span class="badge muted" id="licenseBadge"><span class="dot"></span><span>not activated</span></span>
      </div>
      <div id="activateForm" style="margin-top:14px;">
        <input id="tokenInput" type="text" placeholder="TWA-XXXX-XXXX-XXXX" autocomplete="off" spellcheck="false">
        <div style="margin-top:10px;display:flex;gap:8px;flex-wrap:wrap;">
          <button id="activateBtn">Activate</button>
          <button class="ghost tiny" id="howBtn" type="button">Where do I find this?</button>
        </div>
        <div class="err-line" id="activateErr" hidden></div>
      </div>
      <div id="licenseDetails" hidden>
        <div class="kv">
          <div class="label">Plan</div><div class="v" id="licPlan">—</div>
          <div class="label">Email</div><div class="v" id="licEmail">—</div>
        </div>
      </div>
    </div>
  </div>

  <!-- Activity log -->
  <div class="card tight">
    <div class="card-head">
      <div>
        <div class="card-title">Activity</div>
        <div class="hint" style="margin-top:2px;">Every voucher you send appears here. Failed sends can be re-tried.</div>
      </div>
      <button class="ghost tiny" id="refreshBtn" title="Refresh now">Refresh</button>
    </div>

    <div class="filterbar">
      <span class="chip active" data-filter="all">All</span>
      <span class="chip" data-filter="pending">Pending</span>
      <span class="chip" data-filter="sent">Sent</span>
      <span class="chip" data-filter="dead">Failed</span>
      <span class="spacer"></span>
      <span class="meta" id="receiptHint" hidden></span>
    </div>

    <div id="activityWrap" style="max-height: 460px; overflow:auto;">
      <table>
        <thead>
          <tr>
            <th style="width:88px;">When</th>
            <th>Recipient</th>
            <th style="width:96px;">Voucher</th>
            <th style="width:120px;">Status</th>
            <th>Detail</th>
            <th style="width:96px;text-align:right;">Action</th>
          </tr>
        </thead>
        <tbody id="activityBody">
          <tr><td colspan="6" class="empty">Loading…</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <details>
    <summary>Diagnostics</summary>
    <pre id="diagPre">loading…</pre>
  </details>

  <div class="footer">All processing runs locally on this PC. No data leaves your machine.</div>
</div>

<div class="toast" id="toast"></div>

<script>
const $ = (id) => document.getElementById(id);

function setBadge(el, kind, text) {
  el.className = "badge " + kind;
  el.innerHTML = '<span class="dot"></span><span>' + text + '</span>';
}
function setPill(el, kind, text) {
  el.className = "status-pill " + kind;
  el.innerHTML = '<span class="dot"></span><span>' + text + '</span>';
}

function toast(msg) {
  const t = $("toast");
  t.textContent = msg;
  t.classList.add("show");
  setTimeout(() => t.classList.remove("show"), 2400);
}

let qrCache = "";
async function refreshQR() {
  try {
    const r = await fetch("/proxy/api/whatsapp/qr");
    if (!r.ok) return;
    const j = await r.json();
    if (j.qr && j.qr !== qrCache) {
      qrCache = j.qr;
      const img = $("qrImg");
      const skel = $("qrSkeleton");
      img.src = j.qr;
      img.alt = "WhatsApp pairing QR";
      img.hidden = false;
      if (skel) skel.classList.add("hidden");
      $("resetQrHint").hidden = true;
    } else if (!j.qr) {
      // Service is in awaiting_qr but whatsmeow hasn't pushed the first code
      // yet — keep the skeleton up, hide any stale image.
      const img = $("qrImg");
      const skel = $("qrSkeleton");
      img.hidden = true;
      img.removeAttribute("src");
      qrCache = "";
      if (skel) skel.classList.remove("hidden");
    }
  } catch (e) { /* ignore */ }
}

// Surface the Reset-QR fallback after a few seconds with no QR — that's
// when users start emailing support. Hidden again as soon as a real QR
// arrives or the state leaves awaiting_qr.
let qrStuckTimer = null;
function armResetHint() {
  if (qrStuckTimer) return;
  qrStuckTimer = setTimeout(() => {
    if (!qrCache) $("resetQrHint").hidden = false;
  }, 6000);
}
function disarmResetHint() {
  if (qrStuckTimer) { clearTimeout(qrStuckTimer); qrStuckTimer = null; }
  $("resetQrHint").hidden = true;
}

let currentFilter = "all";
document.querySelectorAll(".chip").forEach(c => {
  c.addEventListener("click", () => {
    document.querySelectorAll(".chip").forEach(x => x.classList.remove("active"));
    c.classList.add("active");
    currentFilter = c.dataset.filter;
    renderActivity(latestItems);
  });
});

function fmtWhen(iso) {
  if (!iso) return "—";
  try {
    const d = new Date(iso);
    const now = new Date();
    const diff = (now - d) / 1000;
    if (diff < 60) return Math.max(1, Math.floor(diff)) + "s ago";
    if (diff < 3600) return Math.floor(diff/60) + "m ago";
    if (diff < 86400) return Math.floor(diff/3600) + "h ago";
    return d.toLocaleDateString();
  } catch (e) { return "—"; }
}

function statusBadge(s) {
  switch (s) {
    case "sent":    return '<span class="badge ok"><span class="dot"></span>Sent</span>';
    case "sending": return '<span class="badge blue"><span class="dot"></span>Sending</span>';
    case "pending": return '<span class="badge warn"><span class="dot"></span>Queued</span>';
    case "dead":    return '<span class="badge err"><span class="dot"></span>Failed</span>';
    default:        return '<span class="badge muted"><span class="dot"></span>' + (s || "?") + '</span>';
  }
}

function voucherTag(v) {
  if (!v) return "—";
  const cls = (v === "receipt" ? "blue" : v === "ledger" ? "muted" : "ok");
  return '<span class="badge ' + cls + '">' + v + '</span>';
}

function detailFor(it) {
  if (it.status === "dead" || (it.status === "pending" && it.attempts > 0)) {
    return '<span class="error">' + escapeHtml(it.last_error || "(no detail)") + '</span>'
      + '<div class="hint" style="margin-top:2px;">' + (it.attempts || 0) + '/' + (it.max_attempts || 8) + ' attempts</div>';
  }
  if (it.kind === "file_with_text" || it.kind === "file") {
    const fn = (it.file_path || "").split(/[\\\\/]/).pop();
    return '<span class="hint">📎 ' + escapeHtml(fn || "—") + '</span>';
  }
  return '<span class="hint">' + escapeHtml((it.text || "").slice(0, 60)) + (it.text && it.text.length > 60 ? "…" : "") + '</span>';
}

function escapeHtml(s) {
  return String(s || "").replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
}

let latestItems = [];

function renderActivity(items) {
  const body = $("activityBody");
  let filtered = items;
  if (currentFilter !== "all") {
    filtered = items.filter(it => {
      if (currentFilter === "pending") return it.status === "pending" || it.status === "sending";
      return it.status === currentFilter;
    });
  }
  if (!filtered.length) {
    body.innerHTML = '<tr><td colspan="6" class="empty">'
      + (items.length ? "No items match this filter." : "No vouchers sent yet. Send one from Tally to see it here.")
      + '</td></tr>';
    return;
  }
  body.innerHTML = filtered.map(it => {
    const canResend = (it.status === "sent" || it.status === "dead" || (it.status === "pending" && it.attempts > 0));
    return '<tr class="row-hover">'
      + '<td class="when" title="' + escapeHtml(it.created_at || "") + '">' + fmtWhen(it.updated_at || it.created_at) + '</td>'
      + '<td class="recipient">+' + escapeHtml(it.recipient || "") + '</td>'
      + '<td>' + voucherTag(it.voucher) + '</td>'
      + '<td>' + statusBadge(it.status) + '</td>'
      + '<td>' + detailFor(it) + '</td>'
      + '<td class="actions">'
        + (canResend
            ? '<button class="secondary tiny" data-resend="' + escapeHtml(it.id) + '">Resend</button>'
            : '<span class="hint">—</span>')
      + '</td>'
      + '</tr>';
  }).join("");

  body.querySelectorAll("button[data-resend]").forEach(btn => {
    btn.addEventListener("click", () => resend(btn.dataset.resend, btn));
  });
}

async function resend(id, btn) {
  if (!id) return;
  btn.disabled = true;
  btn.textContent = "…";
  try {
    const r = await fetch("/proxy/api/queue/item/" + encodeURIComponent(id) + "/resend", {method: "POST"});
    const j = await r.json().catch(() => ({}));
    if (r.ok && j.success) {
      toast("Queued for resend");
      refreshActivity();
    } else {
      toast(j.error || "Resend failed");
      btn.disabled = false; btn.textContent = "Resend";
    }
  } catch (e) {
    toast("Resend failed: " + e.message);
    btn.disabled = false; btn.textContent = "Resend";
  }
}

async function refreshActivity() {
  try {
    const r = await fetch("/proxy/api/queue/list?limit=200");
    if (!r.ok) return;
    const j = await r.json();
    latestItems = j.items || [];
    renderActivity(latestItems);

    // Receipt cooldown hint.
    const hint = $("receiptHint");
    if (j.next_receipt_ready_at) {
      const t = new Date(j.next_receipt_ready_at);
      const secs = Math.max(0, Math.floor((t - new Date()) / 1000));
      const queuedReceipts = latestItems.filter(x => x.voucher === "receipt" && (x.status === "pending" || x.status === "sending")).length;
      if (queuedReceipts > 1 && secs > 0) {
        hint.hidden = false;
        hint.textContent = "Receipts are paced ~90s apart · next in " + secs + "s";
      } else {
        hint.hidden = true;
      }
    }
  } catch (e) { /* ignore */ }
}

async function refreshStatus() {
  try {
    const r = await fetch("/status");
    const s = await r.json();
    $("diagPre").textContent = JSON.stringify(s, null, 2);
    paint(s);
  } catch (e) {
    $("subhead").textContent = "Lost connection to the local service.";
    setPill($("topBadge"), "err", "disconnected");
  }
}

function paint(s) {
  // Header pill + version.
  if (s.service_version) $("brandVer").textContent = "v" + s.service_version;

  // Service.
  if (!s.reachable) {
    setPill($("topBadge"), "err", "service offline");
    $("subhead").textContent = "Service is not running. " + (s.last_error || "");
    return;
  }

  // KPIs.
  $("kpiPending").textContent = s.queue_pending || 0;
  $("kpiSending").textContent = s.queue_sending || 0;
  $("kpiSent").textContent    = s.queue_sent || 0;
  $("kpiDead").textContent    = s.queue_dead || 0;

  // License card.
  if (s.activated) {
    setBadge($("licenseBadge"), "ok", "activated");
    $("licenseLabel").textContent = "Activated";
    $("activateForm").hidden = true;
    $("licenseDetails").hidden = false;
    $("licPlan").textContent = s.license_edition || "—";
    $("licEmail").textContent = s.license_email || "—";
  } else {
    setBadge($("licenseBadge"), "warn", "not activated");
    $("licenseLabel").textContent = "Enter your activation key to start sending";
    $("activateForm").hidden = false;
    $("licenseDetails").hidden = true;
  }

  // WhatsApp card.
  const st = s.whatsapp_state || "unknown";
  switch (st) {
    case "connected":
      setBadge($("waBadge"), "ok", "connected");
      $("waLabel").textContent = "Connected — ready to send";
      $("waPhone").hidden = !s.whatsapp_phone;
      $("waPhone").textContent = s.whatsapp_phone ? "+" + s.whatsapp_phone : "";
      $("qrPanel").hidden = true;
      $("connectedPanel").hidden = false;
      $("logoutBtn").hidden = false;
      $("reconnectBtn").hidden = true;
      disarmResetHint();
      break;
    case "awaiting_qr":
      setBadge($("waBadge"), "warn", "scan QR");
      $("waLabel").textContent = "Scan the QR with WhatsApp on your phone";
      $("waPhone").hidden = true;
      $("qrPanel").hidden = false;
      $("connectedPanel").hidden = true;
      refreshQR();
      if (!qrCache) armResetHint();
      break;
    case "logged_out":
      setBadge($("waBadge"), "err", "logged out");
      $("waLabel").textContent = "Logged out. Click Reconnect to scan a new QR.";
      $("waPhone").hidden = true;
      $("qrPanel").hidden = true;
      $("connectedPanel").hidden = false;
      $("logoutBtn").hidden = true;
      $("reconnectBtn").hidden = false;
      disarmResetHint();
      break;
    case "connecting":
    case "disconnected":
      setBadge($("waBadge"), "muted", "connecting");
      $("waLabel").textContent = "Connecting to WhatsApp…";
      $("waPhone").hidden = true;
      $("qrPanel").hidden = true;
      $("connectedPanel").hidden = true;
      disarmResetHint();
      break;
    default:
      setBadge($("waBadge"), "muted", st);
      $("waLabel").textContent = "Waiting…";
      disarmResetHint();
  }

  // Top pill summarises everything.
  if (!s.activated) {
    setPill($("topBadge"), "warn", "needs activation");
  } else if (st === "connected") {
    setPill($("topBadge"), "ok", "ready to send");
  } else if (st === "awaiting_qr") {
    setPill($("topBadge"), "warn", "scan QR");
  } else {
    setPill($("topBadge"), "warn", st);
  }
  $("subhead").textContent = "Service running locally on this PC";
}

$("activateBtn").addEventListener("click", async () => {
  const tok = $("tokenInput").value.trim();
  $("activateErr").hidden = true;
  if (!tok) {
    $("activateErr").textContent = "Please paste the activation key from your purchase email.";
    $("activateErr").hidden = false;
    return;
  }
  $("activateBtn").disabled = true;
  try {
    const r = await fetch("/proxy/api/activate", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({token: tok}),
    });
    const j = await r.json();
    if (!r.ok || !j.success) {
      $("activateErr").textContent = j.error || j.message || "Activation failed.";
      $("activateErr").hidden = false;
    } else {
      toast("Activated! You're ready to send.");
      $("tokenInput").value = "";
      refreshStatus();
    }
  } catch (e) {
    $("activateErr").textContent = "Could not reach the service. Is it running?";
    $("activateErr").hidden = false;
  } finally {
    $("activateBtn").disabled = false;
  }
});

$("logoutBtn").addEventListener("click", async () => {
  if (!confirm("Log out of WhatsApp on this PC? You'll need to scan the QR again.")) return;
  try {
    const r = await fetch("/proxy/api/whatsapp/logout", {method: "POST"});
    if (r.ok) {
      toast("Logged out. Loading new QR…");
      refreshStatus();
    } else {
      toast("Logout failed.");
    }
  } catch (e) {
    toast("Logout failed: " + e.message);
  }
});

$("reconnectBtn").addEventListener("click", async () => {
  $("reconnectBtn").disabled = true;
  $("reconnectBtn").textContent = "Reconnecting…";
  // Surface the QR skeleton up-front so the user sees movement.
  const skel = $("qrSkeleton");
  if (skel) skel.classList.remove("hidden");
  $("qrImg").hidden = true;
  $("qrImg").removeAttribute("src");
  qrCache = "";

  try {
    const r = await fetch("/proxy/api/whatsapp/reconnect", {method: "POST"});
    if (!r.ok) {
      // Soft reconnect refused — go straight to hard restart.
      await hardRestart();
      return;
    }
    toast("Reconnecting…");

    // Watchdog: if 7s elapse and we still have no QR while state is
    // awaiting_qr, the in-process re-arm didn't take. Fall back to a
    // full service restart, which Windows SCM auto-revives in ~10s.
    setTimeout(async () => {
      try {
        const sr = await fetch("/status");
        const s = await sr.json();
        const stillStuck = s.reachable && s.whatsapp_state === "awaiting_qr" && !qrCache;
        if (stillStuck) {
          await hardRestart();
        }
      } catch { /* ignore */ }
    }, 7000);
  } catch (e) {
    toast("Reconnect failed: " + e.message);
  } finally {
    setTimeout(() => {
      $("reconnectBtn").disabled = false;
      $("reconnectBtn").textContent = "Reconnect";
    }, 1500);
  }
});

// hardRestart asks the service to exit; Windows SCM brings it back in
// ~10 seconds via the failure-restart action defined in the MSI.
async function hardRestart() {
  toast("Restarting service — refresh in ~15 seconds.");
  try {
    await fetch("/proxy/api/service/restart", {method: "POST"});
  } catch { /* the service exits before flushing the response — expected */ }
  // Surface a more visible banner so the user knows to wait.
  const skel = $("qrSkeleton");
  if (skel) {
    skel.querySelector(".lbl").textContent = "Service restarting…";
  }
  // Reload the page after 15s — by then SCM has revived us.
  setTimeout(() => location.reload(), 15000);
}

$("howBtn").addEventListener("click", () => {
  alert("Your activation key was emailed to you after purchase. It looks like TWA-XXXX-XXXX-XXXX. If you can't find it, check spam or email admin@variantstudio.in.");
});

// Reset QR — same path as the watchdog inside reconnectBtn, but exposed
// directly so users don't have to wait through a soft-reconnect attempt
// that's already failed for them.
$("resetQrBtn").addEventListener("click", async () => {
  if (!confirm("Restart the TallyWhatsApp service to reset the QR? The dashboard will reload in about 15 seconds.")) return;
  $("resetQrBtn").disabled = true;
  $("resetQrBtn").textContent = "Restarting…";
  await hardRestart();
});

// Copy buttons next to the manual PowerShell / CMD commands.
document.querySelectorAll("[data-copy]").forEach(btn => {
  btn.addEventListener("click", async () => {
    const target = $(btn.getAttribute("data-copy"));
    if (!target) return;
    const text = target.textContent.trim();
    try {
      await navigator.clipboard.writeText(text);
      const orig = btn.textContent;
      btn.textContent = "Copied";
      setTimeout(() => { btn.textContent = orig; }, 1400);
    } catch {
      toast("Copy failed — select the text manually.");
    }
  });
});

$("refreshBtn").addEventListener("click", () => { refreshStatus(); refreshActivity(); });

setInterval(refreshStatus, 2000);
setInterval(refreshActivity, 3000);
refreshStatus();
refreshActivity();
</script>
</body>
</html>
`
