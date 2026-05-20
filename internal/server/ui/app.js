// Salience dashboard — modern UI, vanilla JS, no framework, no build.
(() => {
  "use strict";

  // ---------- tiny DOM helpers ----------
  const $ = (s) => document.querySelector(s);
  const $$ = (s) => Array.from(document.querySelectorAll(s));
  const el = (html) => {
    const t = document.createElement("template");
    t.innerHTML = html.trim();
    return t.content.firstChild;
  };

  // ---------- state ----------
  const state = {
    projects: [],
    selectedProject: null,
    runs: [],
    selectedRun: null,
    view: "dashboard", // dashboard | mentions | sources | playbook | trend | settings
    cache: {}, // per run cached payload
    jobs: [],
    sse: null,
  };

  // ---------- format helpers ----------
  const pct = (f) => `${(f * 100).toFixed(0)}%`;
  const signedPct = (f) => {
    if (Math.abs(f) < 0.005) return "0%";
    return (f > 0 ? "+" : "−") + pct(Math.abs(f));
  };
  const escape = (s) => String(s == null ? "" : s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  const clip = (s, n) => (s && s.length > n) ? s.slice(0, n - 1) + "…" : (s || "");
  const fmtTime = (iso) => {
    const d = new Date(iso);
    const diff = (Date.now() - d.getTime()) / 1000;
    if (diff < 60) return "just now";
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return d.toLocaleDateString();
  };
  const fmtFullTime = (iso) => new Date(iso).toLocaleString();

  // ---------- API ----------
  const api = {
    runs:    () => fetch("/api/runs").then(r => r.json()),
    run:     (id) => cached(`run:${id}`, () => fetch(`/api/runs/${id}`).then(r => r.json())),
    mentions:(id) => fetch(`/api/runs/${id}/mentions`).then(r => r.json()),
    sources: (id) => cached(`sources:${id}`, () => fetch(`/api/runs/${id}/sources`).then(r => r.json())),
    explanations:(id) => fetch(`/api/runs/${id}/explanations`).then(r => r.json()),
    advice:  (id) => fetch(`/api/runs/${id}/advice`).then(r => r.json()),
    trend:   () => fetch("/api/trend").then(r => r.json()),
    // projects
    projects:    () => fetch("/api/projects").then(r => r.json()),
    project:     (id) => fetch(`/api/projects/${id}`).then(r => r.json()),
    projectRuns: (id) => fetch(`/api/projects/${id}/runs`).then(r => r.json()),
    projectEstimate: (id) => fetch(`/api/projects/${id}/estimate`).then(r => r.json()),
    createProject: (body) => fetch("/api/projects", { method: "POST", headers: {"Content-Type":"application/json"}, body: JSON.stringify(body) }).then(handleErr),
    updateProject: (id, body) => fetch(`/api/projects/${id}`, { method: "PUT", headers: {"Content-Type":"application/json"}, body: JSON.stringify(body) }).then(handleErr),
    deleteProject: (id) => fetch(`/api/projects/${id}`, { method: "DELETE" }).then(r => { if (!r.ok) throw new Error("delete failed"); }),
    runBench:   (id, opts) => fetch(`/api/projects/${id}/bench` + qs(opts), { method: "POST" }).then(handleErr),
    runExplain: (id, opts) => fetch(`/api/projects/${id}/explain` + qs(opts), { method: "POST" }).then(handleErr),
    runAdvise:  (id, opts) => fetch(`/api/projects/${id}/advise` + qs(opts), { method: "POST" }).then(handleErr),
    jobs: () => fetch("/api/jobs").then(r => r.json()),
  };
  function qs(o) { if (!o) return ""; const p = new URLSearchParams(); for (const k in o) p.set(k, o[k]); return "?" + p.toString(); }
  async function handleErr(r) {
    if (!r.ok) {
      const txt = await r.text();
      throw new Error(txt || `HTTP ${r.status}`);
    }
    return r.json();
  }
  function cached(key, loader) {
    if (state.cache[key]) return Promise.resolve(state.cache[key]);
    return loader().then(v => { state.cache[key] = v; return v; });
  }
  function invalidateCacheForRun(id) {
    delete state.cache[`run:${id}`];
    delete state.cache[`sources:${id}`];
  }

  // ---------- charts (inline SVG, no libraries) ----------
  function donut(values, opts) {
    // values: [{label, value, color}]
    const total = values.reduce((s, v) => s + v.value, 0);
    if (total === 0) return `<svg class="donut-svg" width="120" height="120"></svg>`;
    const r = 50, cx = 60, cy = 60, w = 16;
    let a = -Math.PI / 2;
    const arcs = values.map(v => {
      const frac = v.value / total;
      const delta = frac * Math.PI * 2;
      const x1 = cx + r * Math.cos(a), y1 = cy + r * Math.sin(a);
      a += delta;
      const x2 = cx + r * Math.cos(a), y2 = cy + r * Math.sin(a);
      const large = delta > Math.PI ? 1 : 0;
      // build a thick arc by combining outer + inner arc
      const ri = r - w;
      const xi1 = cx + ri * Math.cos(a - delta), yi1 = cy + ri * Math.sin(a - delta);
      const xi2 = cx + ri * Math.cos(a), yi2 = cy + ri * Math.sin(a);
      const d = [
        `M ${x1} ${y1}`,
        `A ${r} ${r} 0 ${large} 1 ${x2} ${y2}`,
        `L ${xi2} ${yi2}`,
        `A ${ri} ${ri} 0 ${large} 0 ${xi1} ${yi1}`,
        "Z"
      ].join(" ");
      return `<path d="${d}" fill="${v.color}"/>`;
    }).join("");
    const center = opts && opts.center ? opts.center : "";
    return `
      <svg class="donut-svg" width="120" height="120" viewBox="0 0 120 120">
        ${arcs}
        <text x="60" y="56" text-anchor="middle" fill="var(--text)" font-size="22" font-weight="700">${escape(center)}</text>
        <text x="60" y="74" text-anchor="middle" fill="var(--muted)" font-size="10" letter-spacing="0.06em" font-weight="600">${escape(opts && opts.sub || "")}</text>
      </svg>`;
  }

  function sparkline(values, w = 110, h = 28, color = "var(--accent-2)") {
    if (!values || values.length === 0) return "";
    const max = Math.max(...values, 0.0001);
    const min = Math.min(...values, 0);
    const range = max - min || 1;
    const step = values.length > 1 ? w / (values.length - 1) : 0;
    const pts = values.map((v, i) =>
      `${(i * step).toFixed(1)},${(h - ((v - min) / range) * h).toFixed(1)}`).join(" ");
    const area = `M 0,${h} L ${pts.split(' ').join(' L ')} L ${w},${h} Z`;
    return `<svg class="stat-spark" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
      <path d="${area}" fill="${color}" opacity="0.10"/>
      <polyline fill="none" stroke="${color}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" points="${pts}"/>
    </svg>`;
  }

  function lineChart(series, w = 760, h = 240) {
    // series: [{ brand, color, points: [{x, y}] }] where x is index 0..n-1, y is 0..1
    if (!series.length || !series[0].points.length) {
      return `<div class="muted" style="padding:30px">No trend data yet — run <code>salience bench</code> a few times to populate this.</div>`;
    }
    const padX = 36, padY = 20;
    const innerW = w - padX * 2;
    const innerH = h - padY * 2;
    const n = series[0].points.length;
    const xAt = (i) => padX + (n === 1 ? innerW / 2 : (i * innerW) / (n - 1));
    const yAt = (v) => padY + innerH - v * innerH;
    const lines = series.map(s => {
      const d = s.points.map((p, i) => (i === 0 ? "M" : "L") + xAt(i) + "," + yAt(p.y)).join(" ");
      return `<path d="${d}" fill="none" stroke="${s.color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`;
    }).join("");
    const dots = series.map(s =>
      s.points.map((p, i) =>
        `<circle cx="${xAt(i)}" cy="${yAt(p.y)}" r="3" fill="${s.color}"/>`).join("")
    ).join("");
    const yLabels = [0, 0.25, 0.5, 0.75, 1].map(v =>
      `<g><line x1="${padX}" x2="${w - padX}" y1="${yAt(v)}" y2="${yAt(v)}" stroke="rgba(255,255,255,0.04)" stroke-width="1"/>` +
      `<text x="${padX - 8}" y="${yAt(v) + 3}" fill="var(--dim)" font-size="10" text-anchor="end" font-family="JetBrains Mono">${(v * 100).toFixed(0)}%</text></g>`
    ).join("");
    const xLabels = series[0].points.map((p, i) =>
      `<text x="${xAt(i)}" y="${h - 4}" fill="var(--dim)" font-size="10" text-anchor="middle" font-family="JetBrains Mono">#${p.runID}</text>`
    ).join("");
    const legend = series.map((s, i) =>
      `<g transform="translate(${padX + i * 130}, 4)">
        <rect width="10" height="10" rx="2" fill="${s.color}"/>
        <text x="16" y="9" fill="var(--text)" font-size="11">${escape(s.brand)}</text>
      </g>`
    ).join("");
    return `<svg class="trend-chart" viewBox="0 0 ${w} ${h}">${yLabels}${xLabels}${lines}${dots}<g transform="translate(0, ${h - 18})">${legend}</g></svg>`;
  }

  // ---------- sidebar ----------
  function renderRunsList() {
    const ol = $("#runsList");
    ol.innerHTML = state.runs.slice(0, 30).map(r => `
      <li data-id="${r.id}" class="${state.selectedRun && state.selectedRun.id === r.id ? "active" : ""}">
        <div class="run-head">
          <span class="id">#${r.id}</span>
          <span class="status-pill ${escape(r.status)}">${escape(r.status)}</span>
        </div>
        <div class="run-meta">${escape(r.brand_name)} · ${escape(fmtTime(r.started_at))}</div>
        <div class="run-stats">
          <span>${r.ok}<span class="muted">ok</span></span>
          <span>${r.errored}<span class="muted">err</span></span>
          <span>$${r.cost.toFixed(4)}</span>
        </div>
      </li>
    `).join("");
    ol.querySelectorAll("li").forEach(li =>
      li.addEventListener("click", () => selectRun(Number(li.dataset.id))));
  }

  function selectRun(id) {
    const r = state.runs.find(x => x.id === id);
    if (!r) return;
    state.selectedRun = r;
    renderRunsList();
    renderMain();
  }

  // ---------- top-level routing ----------
  function setupNav() {
    $$(".tab-link").forEach(a => a.addEventListener("click", () => {
      state.view = a.dataset.view;
      $$(".tab-link").forEach(x => x.classList.toggle("active", x === a));
      renderMain();
    }));
  }

  function renderMain() {
    const main = $("#content");
    if (!state.selectedRun && state.runs.length === 0) {
      main.innerHTML = renderEmpty();
      return;
    }
    if (!state.selectedRun) {
      selectRun(state.runs[0].id);
      return;
    }
    switch (state.view) {
      case "dashboard": return renderDashboard(main);
      case "mentions":  return renderMentionsView(main);
      case "sources":   return renderSourcesView(main);
      case "playbook":  return renderPlaybookView(main);
      case "trend":     return renderTrendView(main);
      case "tools":     return renderToolsView(main);
      case "settings":  return renderSettingsView(main);
    }
  }

  function renderEmpty() {
    return `<div class="empty">
      <div class="icon">
        <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6">
          <path d="M12 2 L4 6 L4 12 C4 17 8 21 12 22 C16 21 20 17 20 12 L20 6 Z"/>
          <circle cx="12" cy="11" r="2.5"/>
        </svg>
      </div>
      <h2>No runs persisted yet</h2>
      <p>Start a benchmark in your terminal: <code>salience bench</code>. The dashboard will pick up the new run automatically — no refresh needed.</p>
    </div>`;
  }

  // ---------- Dashboard view (the main one) ----------
  async function renderDashboard(main) {
    main.innerHTML = `<div class="loading">Loading…</div>`;
    const [data, trendPts] = await Promise.all([
      api.run(state.selectedRun.id),
      api.trend(),
    ]);

    // ----- compute hero stats -----
    const yourRate = data.Totals.Rates[data.UserBrand] || 0;
    const compRates = (data.Competitors || []).map(c => ({
      name: c, rate: data.Totals.Rates[c] || 0
    })).sort((a, b) => b.rate - a.rate);
    const topComp = compRates[0] || { name: "—", rate: 0 };
    const losingCount = (data.Cells || []).filter(c => c.Gap < -0.005).length;
    const totalCount  = (data.Cells || []).length;

    // brand sparkline across runs
    const userSpark = trendPts.map(p => p.rates[data.UserBrand] || 0);
    const trendDelta = userSpark.length >= 2
      ? userSpark[userSpark.length - 1] - userSpark[userSpark.length - 2]
      : 0;

    // hero summary in plain English
    const hero = `
      <div class="hero">
        <div>
          <h1 class="hero-title">${escape(data.UserBrand)}</h1>
          <p class="hero-sub">
            Your brand was mentioned in <strong>${pct(yourRate)}</strong> of LLM answers across
            <strong>${data.TotalSamples}</strong> sample(s).
            ${topComp.rate > yourRate
              ? `Your top competitor <strong>${escape(topComp.name)}</strong> wins <strong class="bad">${pct(topComp.rate)}</strong>.`
              : `You are leading or tied with every tracked competitor.`}
            You are losing on <strong class="${losingCount > 0 ? "bad" : "good"}">${losingCount} of ${totalCount}</strong> prompts.
          </p>
        </div>
        <div class="hero-meta">
          <div><b>Run #${data.RunID}</b></div>
          <div>${escape(data.Started)}</div>
          <div>${escape(data.Status)}</div>
        </div>
      </div>
    `;

    // ----- top stat cards -----
    const stats = `
      <div class="stat-grid">
        <div class="stat accent">
          <div class="stat-label">Your mention rate</div>
          <div class="stat-value">${pct(yourRate)}</div>
          <div class="stat-sub">
            ${trendDelta === 0 ? '<span class="muted">no change vs prev run</span>'
              : trendDelta > 0 ? `<span class="delta-up">▲ ${pct(Math.abs(trendDelta))}</span> <span class="muted">vs prev run</span>`
              : `<span class="delta-down">▼ ${pct(Math.abs(trendDelta))}</span> <span class="muted">vs prev run</span>`}
          </div>
          ${userSpark.length > 1 ? sparkline(userSpark, 90, 24) : ""}
        </div>
        <div class="stat">
          <div class="stat-label">Top competitor</div>
          <div class="stat-value">${pct(topComp.rate)}</div>
          <div class="stat-sub muted">${escape(topComp.name)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">Prompts losing</div>
          <div class="stat-value">${losingCount}<span class="muted" style="font-size:18px"> / ${totalCount}</span></div>
          <div class="stat-sub muted">${totalCount - losingCount} won or tied</div>
        </div>
        <div class="stat">
          <div class="stat-label">Total samples</div>
          <div class="stat-value">${data.TotalSamples}</div>
          <div class="stat-sub muted">${data.TotalFailures} errored</div>
        </div>
      </div>
    `;

    // ----- brand-bar chart -----
    const allBrands = [{ name: data.UserBrand, rate: yourRate, you: true }]
      .concat(compRates.map(c => ({ name: c.name, rate: c.rate })));
    const brandBars = `
      <div class="card">
        <div class="card-head">
          <h3>Mention rate by brand</h3>
          <span class="card-hint">Across all prompts in this run</span>
        </div>
        <div class="help">
          <b>What this shows:</b> for every prompt in this run, how often each brand showed up
          in the LLM's answer (text or cited URL). 100% means the LLM mentioned that brand in every
          single sample.
        </div>
        <div class="brand-bars">
          ${allBrands.map(b => `
            <div class="brand-bar ${b.you ? "you" : "competitor"}">
              <div class="name">${escape(b.name)}${b.you ? `<span class="you-tag">YOU</span>` : ""}</div>
              <div class="bar-track">
                <div class="bar-fill" style="width: ${(b.rate * 100).toFixed(1)}%"></div>
              </div>
              <div class="pct">${pct(b.rate)}</div>
            </div>
          `).join("")}
        </div>
      </div>
    `;

    // ----- sentiment donut -----
    const sentMentions = await api.mentions(state.selectedRun.id);
    const sentCounts = { positive: 0, neutral: 0, negative: 0 };
    sentMentions.forEach(m => {
      if (m.Brand !== data.UserBrand) return;
      const s = m.Sentiment || "neutral";
      sentCounts[s] = (sentCounts[s] || 0) + 1;
    });
    const sentTotal = sentCounts.positive + sentCounts.neutral + sentCounts.negative;
    const donutCenter = sentTotal === 0 ? "—" : pct((sentCounts.positive) / sentTotal);
    const donutSub = sentTotal === 0 ? "no mentions" : "positive";

    // Sentiment colors picked from the active theme palette.
    const cs = getComputedStyle(document.documentElement);
    const colGood = cs.getPropertyValue("--good").trim() || "#8aa86e";
    const colBad  = cs.getPropertyValue("--bad").trim()  || "#d04419";
    const colMid  = cs.getPropertyValue("--bg-3").trim() || "#403d39";
    const donutValues = [
      { label: "Positive", value: sentCounts.positive, color: colGood },
      { label: "Neutral",  value: sentCounts.neutral,  color: colMid  },
      { label: "Negative", value: sentCounts.negative, color: colBad  },
    ];

    const sentimentCard = `
      <div class="card">
        <div class="card-head">
          <h3>Your sentiment</h3>
          <span class="card-hint">How LLMs talk about you</span>
        </div>
        <div class="help">
          <b>Why this matters:</b> a 100% mention rate isn't useful if the LLM is warning users
          against you. Each detected mention is scored from the sentence around it.
        </div>
        <div class="donut-wrap">
          ${donut(donutValues, { center: donutCenter, sub: donutSub })}
          <div class="donut-legend">
            ${donutValues.map(v => `
              <div class="row">
                <span class="sw" style="background:${v.color}"></span>
                <span class="label">${v.label}</span>
                <span class="val">${v.value}</span>
              </div>
            `).join("")}
          </div>
        </div>
      </div>
    `;

    // ----- prompts table -----
    const cells = (data.Cells || []).slice().sort((a, b) => a.Gap - b.Gap);
    const compHead = (data.Competitors || []).map(c => `<th>${escape(c)}</th>`).join("");
    const compHeaderCols = data.Competitors || [];
    const rows = cells.map(c => {
      const klass = c.Gap < -0.005 ? "behind" : c.Gap > 0.005 ? "ahead" : "";
      const lowN = c.Samples < 10 ? `<span class="low-n" title="Statistical warning">n=${c.Samples}</span>` : "";
      const userR = c.Rates[data.UserBrand] || 0;
      const lo = (c.CILow && c.CILow[data.UserBrand]) || 0;
      const hi = (c.CIHigh && c.CIHigh[data.UserBrand]) || 0;
      return `<tr>
        <td class="prompt" title="${escape(c.Prompt)}">${escape(clip(c.Prompt, 60))}</td>
        <td class="muted">${escape(c.ProviderName)}</td>
        <td class="num">${c.Samples}${lowN}</td>
        <td class="num">${pct(userR)}<span class="ci">${pct(lo)}–${pct(hi)}</span></td>
        ${compHeaderCols.map(comp => `<td class="num">${pct(c.Rates[comp] || 0)}</td>`).join("")}
        <td class="num gap ${klass}">${signedPct(c.Gap)}</td>
      </tr>`;
    }).join("");

    const lowAny = cells.some(c => c.Samples < 10);
    const promptTable = `
      <div class="card">
        <div class="card-head">
          <h3>Per-prompt detail</h3>
          <span class="card-hint">Worst gap first</span>
        </div>
        ${lowAny ? `<div class="help">
          <b>⚠ Low sample sizes:</b> some rows have n &lt; 10. The CI column shows the 95% confidence
          interval — when it's wide, the percentage is unreliable. Bump <code>samples_per_prompt</code>
          to ≥10 for tighter bounds.
        </div>` : ""}
        <table class="prompts">
          <thead>
            <tr>
              <th>Prompt</th><th>Provider</th><th>n</th>
              <th>${escape(data.UserBrand)}</th>
              ${compHead}
              <th>Gap</th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;

    main.innerHTML = hero + stats +
      `<div class="cards-grid">${brandBars}${sentimentCard}</div>` +
      promptTable;
  }

  // ---------- Mentions view ----------
  async function renderMentionsView(main) {
    main.innerHTML = `<div class="loading">Loading mentions…</div>`;
    const ms = await api.mentions(state.selectedRun.id);
    if (!ms || ms.length === 0) {
      main.innerHTML = `<div class="empty">
        <div class="icon">
          <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6">
            <circle cx="12" cy="12" r="9"/><path d="M9 9h6M9 13h6M9 17h3"/>
          </svg>
        </div>
        <h2>No mentions yet in run #${state.selectedRun.id}</h2>
        <p>Mentions appear as soon as a sample finishes detection.</p>
      </div>`;
      return;
    }
    const help = `<div class="help">
      <b>Every brand mention</b> the detector found in this run. <b>text</b> = appeared in the
      model's answer; <b>source</b> = appeared in a grounded URL the model cited. Sentiment is
      classified from the sentence around the hit.
    </div>`;
    const items = ms.map(m => `
      <div class="mention-item">
        <div class="left">
          <div class="brand">${escape(m.Brand)}</div>
          <div class="meta">${escape(m.ProviderName)} · #${m.SampleIdx}</div>
          <div class="meta">${escape(m.Where)}${m.IsDomain ? " · domain" : ""}</div>
          <span class="sent-pill ${escape(m.Sentiment || "neutral")}">${escape(m.Sentiment || "neutral")}</span>
        </div>
        <div class="ctx">${escape(m.Context || "—")}</div>
      </div>
    `).join("");

    main.innerHTML = `
      <div class="hero">
        <div>
          <h1 class="hero-title">Mentions</h1>
          <p class="hero-sub"><strong>${ms.length}</strong> brand mention(s) detected in run #${state.selectedRun.id}.</p>
        </div>
      </div>
      ${help}
      <div class="card no-pad"><div class="mention-feed">${items}</div></div>
    `;
  }

  // ---------- Sources view ----------
  async function renderSourcesView(main) {
    main.innerHTML = `<div class="loading">Loading sources…</div>`;
    const s = await api.sources(state.selectedRun.id);
    const help = `<div class="help">
      <b>Where the LLM looked.</b> Every URL cited by the model is grouped by domain and
      categorized. The "<b>source gap</b>" section is the most actionable: domains driving
      competitor mentions that your brand never co-occurs with — concrete pages to be on.
    </div>`;

    const domains = (s.Domains || []).slice(0, 20);
    const gap = (s.MissingFromBrand || []).slice(0, 15);

    const brandChip = (name, count, isYou) =>
      `<span class="brand-chip ${isYou ? "you" : ""}">${escape(name)}<span class="x">×</span>${count}</span>`;

    const domRows = domains.map((d, i) => {
      const chips = Object.entries(d.Brands || {})
        .sort((a, b) => b[1] - a[1]).slice(0, 4)
        .map(([k, v]) => brandChip(k, v, k === s.Brand)).join("");
      return `<div class="dom-row">
        <div class="rank">${i + 1}</div>
        <div class="domain"><span class="url-label">${escape(d.Domain)}</span></div>
        <div class="flex-row">
          <span class="cat-tag ${escape(d.Category)}">${escape(d.Category)}</span>
          <div class="brands">${chips}</div>
        </div>
        <div class="count">${d.Count}</div>
      </div>`;
    }).join("");

    const gapRows = gap.map((d, i) => {
      const chips = Object.entries(d.Brands || {})
        .sort((a, b) => b[1] - a[1]).map(([k, v]) => brandChip(k, v, false)).join("");
      return `<div class="dom-row">
        <div class="rank">${i + 1}</div>
        <div class="domain"><span class="url-label">${escape(d.Domain)}</span></div>
        <div class="flex-row">
          <span class="cat-tag ${escape(d.Category)}">${escape(d.Category)}</span>
          <div class="brands">${chips}</div>
        </div>
        <div class="count">${d.Count}</div>
      </div>`;
    }).join("");

    main.innerHTML = `
      <div class="hero">
        <div>
          <h1 class="hero-title">Sources</h1>
          <p class="hero-sub">
            ${(s.URLs || []).length} unique URL(s) cited across ${(s.Domains || []).length} domain(s).
            ${gap.length > 0 ? `<strong class="bad">${gap.length}</strong> domain(s) drive competitor mentions without co-occurring with you.` : `You co-occur with every cited domain.`}
          </p>
        </div>
      </div>
      ${help}
      <div class="card">
        <div class="card-head">
          <h3>Source gap</h3>
          <span class="card-hint">Concrete to-do list</span>
        </div>
        ${gap.length > 0 ? gapRows : `<p class="muted" style="padding:14px 0">No gaps — you co-occur with every cited domain.</p>`}
      </div>
      <div class="card" style="margin-top:14px">
        <div class="card-head">
          <h3>Top cited domains</h3>
          <span class="card-hint">All brands combined</span>
        </div>
        ${domRows || `<p class="muted">No citations recorded.</p>`}
      </div>
    `;
  }

  // ---------- Playbook view ----------
  async function renderPlaybookView(main) {
    main.innerHTML = `<div class="loading">Loading playbook…</div>`;
    const [explanations, advice, data] = await Promise.all([
      api.explanations(state.selectedRun.id),
      api.advice(state.selectedRun.id),
      api.run(state.selectedRun.id),
    ]);

    const help = `<div class="help">
      <b>Action plan combining sources + LLM-stated reasons + LLM-stated actions.</b><br>
      To populate this, run <code>salience explain</code> and <code>salience advise</code> in your terminal.
      Each call probes the LLM with a follow-up question about your competitors.
    </div>`;

    // group by losing prompt
    const losers = (data.Cells || []).filter(c => c.Gap < -0.005)
      .sort((a, b) => a.Gap - b.Gap);

    if (losers.length === 0) {
      main.innerHTML = `${heroFor("Playbook")}<div class="card"><p class="muted">No losing prompts — playbook empty. You're matching or beating every competitor in this run.</p></div>`;
      return;
    }

    const adviceByPrompt = {};
    (advice || []).forEach(a => {
      if (!a.Advice) return;
      (adviceByPrompt[a.Prompt] = adviceByPrompt[a.Prompt] || []).push(a);
    });
    const expByBrand = {};
    (explanations || []).forEach(e => {
      if (!e.Reasoning) return;
      (expByBrand[e.AskedAboutBrand] = expByBrand[e.AskedAboutBrand] || []).push(e);
    });

    const cards = losers.map(c => {
      const winnerName = (data.Competitors || [])
        .map(name => ({ name, rate: c.Rates[name] || 0 }))
        .sort((a, b) => b.rate - a.rate)[0];
      const reasons = winnerName ? (expByBrand[winnerName.name] || []) : [];
      const acts = adviceByPrompt[c.Prompt] || [];
      const reasonList = reasons.slice(0, 3).map(r =>
        `<li>${escape(clip(r.Reasoning, 240))} <span class="muted" style="font-size:11px"> · ${escape(r.ProviderName)}</span></li>`
      ).join("");
      const actList = acts.slice(0, 3).map(a =>
        `<li>${escape(clip(a.Advice, 360))} <span class="muted" style="font-size:11px"> · ${escape(a.ProviderName)}</span></li>`
      ).join("");
      return `<div class="action-card">
        <div class="losing-row">
          <h4>${escape(c.Prompt)}</h4>
          <span class="gap-large">${signedPct(c.Gap)}</span>
        </div>
        <div class="muted" style="font-size:12px">
          Provider: ${escape(c.ProviderName)} ·
          You: <strong>${pct(c.Rates[data.UserBrand] || 0)}</strong> ·
          Top competitor: <strong>${escape(winnerName ? winnerName.name : "—")} ${winnerName ? pct(winnerName.rate) : ""}</strong>
        </div>
        ${reasonList ? `<div class="action-section">
          <div class="label">Why the LLMs picked ${escape(winnerName ? winnerName.name : "")}</div>
          <ul class="action-list">${reasonList}</ul>
        </div>` : ""}
        ${actList ? `<div class="action-section">
          <div class="label">Recommended actions</div>
          <ul class="action-list">${actList}</ul>
        </div>` : `<div class="action-section">
          <div class="empty-actions">No actions yet. Run <code>salience advise -run ${state.selectedRun.id}</code> to generate them.</div>
        </div>`}
      </div>`;
    }).join("");

    main.innerHTML = `${heroFor("Playbook", `${losers.length} losing prompt(s) ranked worst-first.`)}${help}${cards}`;
  }

  // ---------- Trend view ----------
  async function renderTrendView(main) {
    main.innerHTML = `<div class="loading">Loading trend…</div>`;
    const pts = await api.trend();
    const help = `<div class="help">
      <b>Mention rate over time.</b> One line per brand, one point per run, oldest on the left.
      Use <code>salience bench</code> on a schedule (e.g. weekly) to populate this.
    </div>`;
    if (!pts.length) {
      main.innerHTML = `${heroFor("Trend")}${help}<div class="card"><p class="muted">No runs yet.</p></div>`;
      return;
    }
    const brands = Object.keys(pts[0].rates || {});
    // Warm palette derived from the active theme.
    const cs = getComputedStyle(document.documentElement);
    const palette = [
      (cs.getPropertyValue("--accent").trim() || "#eb5e28"),
      (cs.getPropertyValue("--good").trim()   || "#8aa86e"),
      (cs.getPropertyValue("--muted").trim()  || "#ccc5b9"),
      (cs.getPropertyValue("--warn").trim()   || "#d4a017"),
      "#a35728",
      "#73776b",
    ];
    const series = brands.map((b, i) => ({
      brand: b,
      color: palette[i % palette.length],
      points: pts.map(p => ({ runID: p.run_id, y: p.rates[b] || 0 })),
    }));

    main.innerHTML = `${heroFor("Trend", `${pts.length} run(s) plotted`)}${help}
      <div class="card">${lineChart(series)}</div>
    `;
  }

  function heroFor(title, sub) {
    return `<div class="hero">
      <div>
        <h1 class="hero-title">${escape(title)}</h1>
        ${sub ? `<p class="hero-sub">${sub}</p>` : ""}
      </div>
      <div class="hero-meta">
        <div><b>Run #${state.selectedRun ? state.selectedRun.id : "—"}</b></div>
      </div>
    </div>`;
  }

  // ---------- v0.2 tools view ----------
  // One sub-tab per derived data table so users can see what `scrape`,
  // `expand`, `brief`, `action`, `schedule`, `watch`, `simulate` produced
  // without leaving the dashboard.

  let toolsTab = "suggestions";

  async function renderToolsView(main) {
    if (!state.selectedProject) {
      main.innerHTML = renderEmpty();
      return;
    }
    const subTabs = [
      ["suggestions", "Prompt suggestions"],
      ["actions",     "Actions log"],
      ["briefs",      "Content briefs"],
      ["scrapes",     "Scraped pages"],
      ["schedules",   "Schedules"],
      ["watchers",    "Watchers"],
      ["simulations", "Simulations"],
    ];
    const tabs = `<div class="tabs">` + subTabs.map(([k, label]) =>
      `<div class="tab ${toolsTab === k ? "active" : ""}" data-sub="${k}">${escape(label)}</div>`
    ).join("") + `</div>`;
    main.innerHTML = `
      <div class="hero">
        <div>
          <h1 class="hero-title">Tools</h1>
          <p class="hero-sub">
            Outputs from the diagnose → prescribe → act → verify pipeline.
            Each tab is a different table — scrape, expand, brief, action,
            schedule, watch, simulate — populated from your CLI runs.
          </p>
        </div>
      </div>
      ${tabs}
      <div id="toolsBody"><div class="loading">Loading…</div></div>
    `;
    document.querySelectorAll(".tab[data-sub]").forEach(el =>
      el.addEventListener("click", () => {
        toolsTab = el.dataset.sub;
        renderToolsView(main);
      }));
    renderToolsBody();
  }

  async function renderToolsBody() {
    const body = $("#toolsBody");
    if (!body) return;
    const pid = state.selectedProject.id;
    try {
      switch (toolsTab) {
        case "suggestions":  return renderToolsSuggestions(body, pid);
        case "actions":      return renderToolsActions(body, pid);
        case "briefs":       return renderToolsBriefs(body, pid);
        case "scrapes":      return renderToolsScrapes(body, pid);
        case "schedules":    return renderToolsSchedules(body, pid);
        case "watchers":     return renderToolsWatchers(body, pid);
        case "simulations":  return renderToolsSimulations(body, pid);
      }
    } catch (e) {
      body.innerHTML = `<div class="card"><p class="bad">Failed to load: ${escape(e.message || String(e))}</p></div>`;
    }
  }

  // ---- Suggestions (prompt expansion) ----

  async function renderToolsSuggestions(body, pid) {
    const items = await fetch(`/api/suggestions?project=${pid}`).then(r => r.json());
    if (!items || items.length === 0) {
      body.innerHTML = toolsEmpty("No prompt suggestions yet.",
        "Run <code>salience expand</code> in your terminal to brainstorm new prompts via an LLM.");
      return;
    }
    body.innerHTML = `<div class="help">
      <b>Staged prompt variations</b> from <code>salience expand</code>. Accept the ones worth adding
      to this project's prompt list — they only count after acceptance.
    </div>` + items.map(s => `
      <div class="card" style="margin-bottom:8px">
        <div style="display:flex;justify-content:space-between;align-items:start;gap:14px">
          <div style="flex:1">
            <div style="font-weight:500">${escape(s.Text)}</div>
            ${s.Rationale ? `<div class="muted" style="font-size:12px;margin-top:3px">${escape(s.Rationale)}</div>` : ""}
          </div>
          <div>
            <span class="sent-pill ${s.Accepted ? "positive" : "neutral"}">${s.Accepted ? "accepted" : "pending"}</span>
          </div>
        </div>
      </div>
    `).join("");
  }

  // ---- Actions log ----

  async function renderToolsActions(body, pid) {
    const actions = await fetch(`/api/actions?project=${pid}`).then(r => r.json());
    if (!actions || actions.length === 0) {
      body.innerHTML = toolsEmpty("No actions logged.",
        "Log operational events with <code>salience action add \"…\"</code>. They overlay onto <b>diff</b> output so you can correlate movements with what your team did.");
      return;
    }
    body.innerHTML = `<div class="card no-pad"><table class="prompts">
      <thead><tr><th>Date</th><th>Description</th><th>Prompts</th><th>Notes</th></tr></thead>
      <tbody>${actions.map(a => `
        <tr>
          <td>${escape(new Date(a.TakenAt).toLocaleDateString())}</td>
          <td>${escape(a.Description)}</td>
          <td class="muted">${escape((a.AppliesToPrompts || []).join(", ") || "—")}</td>
          <td class="muted">${escape(a.Notes || "")}</td>
        </tr>
      `).join("")}</tbody>
    </table></div>`;
  }

  // ---- Content briefs ----

  async function renderToolsBriefs(body, pid) {
    const briefs = await fetch(`/api/briefs?project=${pid}`).then(r => r.json());
    if (!briefs || briefs.length === 0) {
      body.innerHTML = toolsEmpty("No content briefs yet.",
        "Generate one with <code>salience brief -prompt \"...\"</code>. Each brief combines scraped competitor content, LLM-stated reasons, and recommended actions into a Markdown action plan.");
      return;
    }
    body.innerHTML = briefs.map(b => `
      <div class="card" style="margin-bottom:10px">
        <div class="card-head">
          <h3>Brief #${b.ID} — ${escape(clip(b.Prompt, 60))}</h3>
          <span class="card-hint">${escape(new Date(b.CreatedAt).toLocaleString())}</span>
        </div>
        <pre style="white-space:pre-wrap;font-size:12.5px;background:var(--bg-2);padding:12px;border-radius:6px;line-height:1.55;font-family:inherit;margin:0">${escape(b.BodyMarkdown)}</pre>
      </div>
    `).join("");
  }

  // ---- Scraped pages ----

  async function renderToolsScrapes(body, pid) {
    const pages = await fetch(`/api/scraped`).then(r => r.json());
    if (!pages || pages.length === 0) {
      body.innerHTML = toolsEmpty("No pages scraped yet.",
        "Run <code>salience scrape -run N</code> to fetch the content of URLs the LLM cited. The body text is then used by briefs and the page-level gap analysis.");
      return;
    }
    body.innerHTML = `<div class="card no-pad"><table class="prompts">
      <thead><tr><th>Status</th><th>URL</th><th>Title</th><th>Fetched</th></tr></thead>
      <tbody>${pages.map(p => {
        const st = p.Err ? "ERR" : String(p.StatusCode);
        return `<tr>
          <td class="num ${p.Err ? "bad" : ""}">${escape(st)}</td>
          <td class="url" style="font-family:JetBrains Mono;font-size:11.5px">${escape(clip(p.URL, 60))}</td>
          <td>${escape(clip(p.Title, 50))}</td>
          <td class="muted">${escape(new Date(p.FetchedAt).toLocaleString())}</td>
        </tr>`;
      }).join("")}</tbody>
    </table></div>`;
  }

  // ---- Schedules ----

  async function renderToolsSchedules(body, pid) {
    const scheds = await fetch(`/api/schedules?project=${pid}`).then(r => r.json());
    if (!scheds || scheds.length === 0) {
      body.innerHTML = toolsEmpty("No schedules.",
        "Add a recurring benchmark with <code>salience schedule add -cron \"0 9 * * MON\"</code>. The dashboard server's ticker fires schedules every 30s while <code>salience serve</code> is running.");
      return;
    }
    body.innerHTML = `<div class="card no-pad"><table class="prompts">
      <thead><tr><th>ID</th><th>Cron</th><th>Enabled</th><th>Next fires</th><th>Last fired</th></tr></thead>
      <tbody>${scheds.map(s => `
        <tr>
          <td class="num">${s.ID}</td>
          <td><code>${escape(s.CronExpr)}</code></td>
          <td>${s.Enabled ? `<span class="sent-pill positive">yes</span>` : `<span class="sent-pill neutral">no</span>`}</td>
          <td class="muted">${escape(new Date(s.NextFires).toLocaleString())}</td>
          <td class="muted">${s.LastFired ? escape(new Date(s.LastFired).toLocaleString()) : "—"}</td>
        </tr>
      `).join("")}</tbody>
    </table></div>`;
  }

  // ---- Watchers ----

  async function renderToolsWatchers(body, pid) {
    const ws = await fetch(`/api/watchers?project=${pid}`).then(r => r.json());
    if (!ws || ws.length === 0) {
      body.innerHTML = toolsEmpty("No watchers.",
        "Track an external URL with <code>salience watch add -url …</code>. The server refetches each watcher on its interval and flags content changes.");
      return;
    }
    body.innerHTML = `<div class="card no-pad"><table class="prompts">
      <thead><tr><th>ID</th><th>Label</th><th>URL</th><th>Interval</th><th>Last fetched</th></tr></thead>
      <tbody>${ws.map(w => `
        <tr>
          <td class="num">${w.ID}</td>
          <td>${escape(w.Label || "—")}</td>
          <td class="url" style="font-family:JetBrains Mono;font-size:11.5px">${escape(clip(w.URL, 50))}</td>
          <td class="muted">${Math.round(w.IntervalSeconds / 60)} min</td>
          <td class="muted">${w.LastFetchedAt ? escape(new Date(w.LastFetchedAt).toLocaleString()) : "—"}</td>
        </tr>
      `).join("")}</tbody>
    </table></div>`;
  }

  // ---- Simulations ----

  async function renderToolsSimulations(body, pid) {
    const sims = await fetch(`/api/simulations?project=${pid}`).then(r => r.json());
    if (!sims || sims.length === 0) {
      body.innerHTML = toolsEmpty("No simulations yet.",
        "Test a content draft against a prompt with <code>salience simulate -prompt \"…\" -content draft.md</code>. The result is the predicted mention-rate change if that content shipped.");
      return;
    }
    body.innerHTML = `<div class="card no-pad"><table class="prompts">
      <thead><tr><th>ID</th><th>Prompt</th><th>Baseline</th><th>Simulated</th><th>Δ</th><th>n</th><th>When</th></tr></thead>
      <tbody>${sims.map(s => {
        const sgn = s.Delta > 0.005 ? "good" : s.Delta < -0.005 ? "bad" : "";
        return `<tr>
          <td class="num">${s.ID}</td>
          <td>${escape(clip(s.Prompt, 60))}</td>
          <td class="num">${pct(s.BaselineRate)}</td>
          <td class="num">${pct(s.SimulatedRate)}</td>
          <td class="num ${sgn}">${signedPct(s.Delta)}</td>
          <td class="num muted">${s.NSamples}</td>
          <td class="muted">${escape(new Date(s.CreatedAt).toLocaleString())}</td>
        </tr>`;
      }).join("")}</tbody>
    </table></div>`;
  }

  function toolsEmpty(title, hint) {
    return `<div class="card">
      <div class="card-head"><h3>${escape(title)}</h3></div>
      <div class="muted" style="font-size:13px;line-height:1.55">${hint}</div>
    </div>`;
  }

  // ---------- project picker ----------
  function renderProjectPicker() {
    const name = $("#projectName");
    const menu = $("#projectMenuItems");
    if (state.selectedProject) {
      name.textContent = state.selectedProject.name;
    } else if (state.projects.length === 0) {
      name.textContent = "Create a project";
    } else {
      name.textContent = "Pick a project";
    }
    menu.innerHTML = state.projects.map(p => `
      <div class="project-menu-item ${state.selectedProject && state.selectedProject.id === p.id ? "active" : ""}" data-id="${p.id}">
        <span>${escape(p.name)}</span>
        <span class="meta">${(p.competitors || []).length} comp · ${(p.prompts || []).length} prompts</span>
      </div>
    `).join("");
    menu.querySelectorAll(".project-menu-item").forEach(div => {
      div.addEventListener("click", () => {
        selectProject(Number(div.dataset.id));
        closeProjectMenu();
      });
    });
  }
  function openProjectMenu()  { $("#projectMenu").classList.remove("hidden"); }
  function closeProjectMenu() { $("#projectMenu").classList.add("hidden"); }

  async function selectProject(id) {
    const p = state.projects.find(x => x.id === id);
    if (!p) return;
    state.selectedProject = p;
    state.selectedRun = null;
    state.cache = {};
    await refreshRunsForProject();
    updateRunNowEnabled();
    renderProjectPicker();
    renderMain();
  }

  async function refreshRunsForProject() {
    if (!state.selectedProject) {
      state.runs = [];
      renderRunsList();
      return;
    }
    state.runs = await api.projectRuns(state.selectedProject.id);
    renderRunsList();
    if (!state.selectedRun && state.runs.length > 0) {
      state.selectedRun = state.runs[0];
    }
  }

  function updateRunNowEnabled() {
    const btn = $("#runNowBtn");
    btn.disabled = !state.selectedProject ||
      !(state.selectedProject.providers || []).length ||
      !(state.selectedProject.prompts || []).length;
  }

  // ---------- modal helpers ----------
  function openModal(title, bodyHTML, opts) {
    opts = opts || {};
    $("#modalTitle").textContent = title;
    $("#modalBody").innerHTML = bodyHTML;
    $("#modalFoot").innerHTML = (opts.footer || "");
    $("#modalBox").classList.toggle("large", !!opts.large);
    $("#modalBackdrop").classList.remove("hidden");
    if (opts.onMount) opts.onMount();
  }
  function closeModal() { $("#modalBackdrop").classList.add("hidden"); }

  // ---------- new / edit project form ----------
  function showProjectForm(existing) {
    const p = existing || {
      name: "", brand: { name: "", aliases: [] },
      competitors: [], prompts: [],
      providers: [{ name: "openai-default", kind: "openai", model: "gpt-4.1-mini" }],
      samples_per_prompt: 5, concurrency_per_provider: 3, max_tokens: 512, notes: ""
    };
    const isEdit = !!existing;

    const html = `
      <div class="form-row">
        <label>Project name</label>
        <input type="text" id="pf_name" value="${escape(p.name)}" placeholder="My CRM tracker">
        <div class="hint">Used as the display name in the sidebar and the slug for URLs.</div>
      </div>
      <div class="form-cols2">
        <div class="form-row">
          <label>Your brand name</label>
          <input type="text" id="pf_brand" value="${escape(p.brand.name)}" placeholder="Northwind">
        </div>
        <div class="form-row">
          <label>Brand aliases (comma-separated)</label>
          <input type="text" id="pf_brand_aliases" value="${escape((p.brand.aliases||[]).join(", "))}" placeholder="northwind.com, ノースウィンド">
        </div>
      </div>
      <div class="form-row">
        <label>Competitors (one per line — format: <code>Name | alias1, alias2</code>)</label>
        <textarea id="pf_competitors" placeholder="Contoso | contoso.com&#10;Fabrikam | fabrikam.com">${escape((p.competitors||[]).map(c => c.name + (c.aliases && c.aliases.length ? " | " + c.aliases.join(", ") : "")).join("\n"))}</textarea>
      </div>
      <div class="form-row">
        <label>Prompts (one per line)</label>
        <textarea id="pf_prompts" placeholder="best CRM for a 20-person SaaS startup&#10;recommend a CRM with strong API support">${escape((p.prompts||[]).join("\n"))}</textarea>
      </div>
      <div class="form-row">
        <label>Providers (one per line — format: <code>kind | name | model</code>)</label>
        <textarea id="pf_providers" placeholder="openai | chatgpt | gpt-4.1-mini&#10;anthropic | claude | claude-haiku-4-5&#10;perplexity | sonar | sonar">${escape((p.providers||[]).map(pr => `${pr.kind} | ${pr.name} | ${pr.model}`).join("\n"))}</textarea>
        <div class="hint">Supported kinds: <code>openai</code>, <code>anthropic</code>, <code>perplexity</code>. API keys come from your environment / .env, never typed here.</div>
      </div>
      <div class="form-cols3">
        <div class="form-row"><label>Samples / prompt</label><input type="number" id="pf_samples" value="${p.samples_per_prompt || 5}" min="1"></div>
        <div class="form-row"><label>Parallelism</label><input type="number" id="pf_parallel" value="${p.concurrency_per_provider || 3}" min="1"></div>
        <div class="form-row"><label>Max tokens</label><input type="number" id="pf_maxtok" value="${p.max_tokens || 512}" min="64"></div>
      </div>
      <div class="form-row">
        <label>Notes (optional)</label>
        <textarea id="pf_notes" style="min-height:60px">${escape(p.notes || "")}</textarea>
      </div>
    `;
    const footer = `
      ${isEdit ? `<button class="btn-danger" id="pf_delete" type="button">Delete project</button>` : ""}
      <div style="flex:1"></div>
      <button class="btn-secondary" id="pf_cancel" type="button">Cancel</button>
      <button class="btn-primary" id="pf_save" type="button">${isEdit ? "Save changes" : "Create project"}</button>
    `;
    openModal(isEdit ? `Edit ${p.name}` : "New project", html, {
      large: true, footer,
      onMount: () => {
        $("#pf_cancel").addEventListener("click", closeModal);
        $("#pf_save").addEventListener("click", () => saveProjectForm(existing));
        if (isEdit) {
          $("#pf_delete").addEventListener("click", () => {
            if (!confirm(`Delete project "${p.name}" and all its runs?`)) return;
            api.deleteProject(p.id).then(() => {
              closeModal();
              toast(`Deleted ${p.name}`);
              state.selectedProject = null;
              refreshAll();
            }).catch(e => toast("Delete failed: " + e.message));
          });
        }
      }
    });
  }

  function parseCompetitors(s) {
    return s.split("\n").map(l => l.trim()).filter(Boolean).map(line => {
      const [n, aliases] = line.split("|").map(x => (x || "").trim());
      return { name: n, aliases: aliases ? aliases.split(",").map(x => x.trim()).filter(Boolean) : [] };
    });
  }
  function parseProviders(s) {
    return s.split("\n").map(l => l.trim()).filter(Boolean).map(line => {
      const parts = line.split("|").map(x => (x || "").trim());
      const [kind, name, model] = parts;
      return { kind: kind || "openai", name: name || "default", model: model || "" };
    });
  }

  async function saveProjectForm(existing) {
    const body = {
      name: $("#pf_name").value.trim(),
      brand: {
        name: $("#pf_brand").value.trim(),
        aliases: $("#pf_brand_aliases").value.split(",").map(x => x.trim()).filter(Boolean),
      },
      competitors: parseCompetitors($("#pf_competitors").value),
      prompts: $("#pf_prompts").value.split("\n").map(l => l.trim()).filter(Boolean),
      providers: parseProviders($("#pf_providers").value),
      samples_per_prompt: Number($("#pf_samples").value) || 5,
      concurrency_per_provider: Number($("#pf_parallel").value) || 3,
      max_tokens: Number($("#pf_maxtok").value) || 512,
      notes: $("#pf_notes").value.trim(),
    };
    if (!body.name || !body.brand.name) {
      toast("Project name and brand name are required");
      return;
    }
    try {
      if (existing) {
        await api.updateProject(existing.id, body);
        toast(`Saved ${body.name}`);
      } else {
        const p = await api.createProject(body);
        toast(`Created ${body.name}`);
        state.selectedProject = p;
      }
      closeModal();
      refreshAll();
    } catch (e) {
      toast(e.message || "Save failed");
    }
  }

  async function refreshAll() {
    state.projects = await api.projects();
    if (state.selectedProject) {
      const fresh = state.projects.find(p => p.id === state.selectedProject.id);
      if (fresh) state.selectedProject = fresh;
      else state.selectedProject = state.projects[0] || null;
    } else {
      state.selectedProject = state.projects[0] || null;
    }
    renderProjectPicker();
    await refreshRunsForProject();
    updateRunNowEnabled();
    renderMain();
  }

  // ---------- run-now flow ----------
  async function showRunNowConfirm() {
    if (!state.selectedProject) return;
    let est;
    try { est = await api.projectEstimate(state.selectedProject.id); }
    catch (e) { toast("Estimate failed: " + e.message); return; }

    const rows = (est.providers || []).map(p => `
      <tr>
        <td>${escape(p.name)}</td>
        <td class="muted">${escape(p.model)}</td>
        <td class="num">${p.calls}</td>
        <td class="num">$${p.cost.toFixed(4)}</td>
      </tr>
    `).join("");

    const html = `
      <p style="margin-top:0">This will spend real API credits. Estimate:</p>
      <table class="prompts">
        <thead><tr><th>Provider</th><th>Model</th><th>Calls</th><th>Cost</th></tr></thead>
        <tbody>${rows}</tbody>
        <tfoot>
          <tr><td colspan="3" style="text-align:right;font-weight:600">Total estimate</td>
              <td class="num" style="font-weight:600">$${(est.total || 0).toFixed(4)}</td></tr>
        </tfoot>
      </table>
      <div class="form-row" style="margin-top:18px">
        <label>Abort if cost exceeds (USD, 0 = no cap)</label>
        <input type="number" id="rn_maxcost" value="${((est.total || 0) * 1.5).toFixed(2)}" step="0.01" min="0">
      </div>
    `;
    const footer = `
      <button class="btn-secondary" id="rn_cancel" type="button">Cancel</button>
      <button class="btn-primary" id="rn_go" type="button">Run benchmark</button>
    `;
    openModal("Confirm benchmark run", html, {
      large: true, footer,
      onMount: () => {
        $("#rn_cancel").addEventListener("click", closeModal);
        $("#rn_go").addEventListener("click", async () => {
          const mc = Number($("#rn_maxcost").value) || 0;
          try {
            await api.runBench(state.selectedProject.id, { max_cost: mc });
            toast(`Benchmark started for ${state.selectedProject.name}`);
            closeModal();
            // jump to dashboard so live progress is visible
            switchView("dashboard");
          } catch (e) {
            toast("Failed to start: " + e.message);
          }
        });
      }
    });
  }

  function switchView(v) {
    state.view = v;
    $$(".tab-link").forEach(x => x.classList.toggle("active", x.dataset.view === v));
    renderMain();
  }

  // ---------- settings view (per project) ----------
  function renderSettingsView(main) {
    if (!state.selectedProject) {
      main.innerHTML = renderEmpty();
      return;
    }
    const p = state.selectedProject;
    main.innerHTML = `
      <div class="hero">
        <div>
          <h1 class="hero-title">${escape(p.name)}</h1>
          <p class="hero-sub">Edit this project's brand, competitors, prompts, and providers. Changes apply to future runs only — past run data isn't rewritten.</p>
        </div>
        <div class="hero-meta">
          <div><b>Slug</b></div><div>${escape(p.slug)}</div>
          <div><b>Updated</b></div><div>${escape(new Date(p.updated_at).toLocaleString())}</div>
        </div>
      </div>

      <div class="cards-grid">
        <div class="card">
          <div class="card-head"><h3>Brand</h3></div>
          <div><strong>${escape(p.brand.name)}</strong></div>
          <div class="muted" style="margin-top:4px">${(p.brand.aliases || []).map(a => `<span class="chip">${escape(a)}</span>`).join(" ") || "<span class='dim'>no aliases</span>"}</div>
        </div>
        <div class="card">
          <div class="card-head"><h3>Knobs</h3></div>
          <div class="muted">Samples / prompt · <strong>${p.samples_per_prompt}</strong></div>
          <div class="muted">Parallelism · <strong>${p.concurrency_per_provider}</strong></div>
          <div class="muted">Max tokens · <strong>${p.max_tokens}</strong></div>
        </div>
      </div>

      <div class="card">
        <div class="card-head"><h3>Competitors (${(p.competitors||[]).length})</h3></div>
        <div class="chip-list">
          ${(p.competitors || []).map(c => `<span class="chip">${escape(c.name)}${c.aliases && c.aliases.length ? ` <span class="dim">— ${escape(c.aliases.join(", "))}</span>` : ""}</span>`).join("")}
          ${!(p.competitors||[]).length ? "<span class='dim'>None yet — edit to add</span>" : ""}
        </div>
      </div>

      <div class="card">
        <div class="card-head"><h3>Prompts (${(p.prompts||[]).length})</h3></div>
        <ul style="margin:0;padding-left:18px">
          ${(p.prompts || []).map(q => `<li style="margin-bottom:4px">${escape(q)}</li>`).join("")}
          ${!(p.prompts||[]).length ? "<li class='dim'>None yet</li>" : ""}
        </ul>
      </div>

      <div class="card">
        <div class="card-head"><h3>Providers (${(p.providers||[]).length})</h3></div>
        <table class="prompts">
          <thead><tr><th>Name</th><th>Kind</th><th>Model</th></tr></thead>
          <tbody>
            ${(p.providers || []).map(pr => `<tr>
              <td>${escape(pr.name)}</td>
              <td class="muted">${escape(pr.kind)}</td>
              <td class="muted" style="font-family:JetBrains Mono">${escape(pr.model)}</td>
            </tr>`).join("")}
          </tbody>
        </table>
      </div>

      <div style="display:flex;gap:8px;margin-top:8px">
        <button class="btn-primary" id="settings_edit" type="button">Edit project</button>
        <button class="btn-secondary" id="settings_explain" type="button">Run explain</button>
        <button class="btn-secondary" id="settings_advise" type="button">Run advise</button>
      </div>
    `;
    $("#settings_edit").addEventListener("click", () => showProjectForm(p));
    $("#settings_explain").addEventListener("click", async () => {
      try { await api.runExplain(p.id, {}); toast("Explain started"); }
      catch (e) { toast("Failed: " + e.message); }
    });
    $("#settings_advise").addEventListener("click", async () => {
      try { await api.runAdvise(p.id, {}); toast("Advise started"); }
      catch (e) { toast("Failed: " + e.message); }
    });
  }

  // ---------- toast ----------
  let toastT = null;
  function toast(msg) {
    const t = $("#toast");
    t.innerHTML = `<span class="dot"></span> ${escape(msg)}`;
    t.classList.remove("hidden");
    clearTimeout(toastT);
    toastT = setTimeout(() => t.classList.add("hidden"), 2400);
  }

  // ---------- SSE ----------
  function startSSE() {
    if (state.sse) state.sse.close();
    const sse = new EventSource("/api/events");
    state.sse = sse;
    const card = document.querySelector(".live-card");
    const status = $("#liveStatus");
    sse.onopen  = () => { card.classList.add("live"); card.classList.remove("error"); status.textContent = "live"; };
    sse.onerror = () => { card.classList.remove("live"); card.classList.add("error"); status.textContent = "disconnected"; };

    sse.addEventListener("run-start", (ev) => {
      try {
        const d = JSON.parse(ev.data);
        toast(`Run #${d.run_id} started`);
        refreshRuns();
      } catch (e) {}
    });
    sse.addEventListener("progress", (ev) => {
      try {
        const d = JSON.parse(ev.data);
        const run = state.runs.find(r => r.id === d.run_id);
        if (run) { run.ok = d.ok; run.errored = d.errored; renderRunsList(); }
        if (state.selectedRun && state.selectedRun.id === d.run_id) {
          invalidateCacheForRun(d.run_id);
          // light refresh every few samples
          if ((d.ok + d.errored) % 5 === 0) renderMain();
        }
      } catch (e) {}
    });
    sse.addEventListener("run-end", (ev) => {
      try {
        const d = JSON.parse(ev.data);
        toast(`Run #${d.run_id} ${d.status}`);
        refreshRuns();
      } catch (e) {}
    });

    // Job events from dashboard-launched bench/explain/advise.
    sse.addEventListener("job", (ev) => {
      try {
        const j = JSON.parse(ev.data);
        if (j.status === "done") toast(`Job ${j.kind} done`);
        else if (j.status === "errored") toast(`Job ${j.kind} errored: ${j.err || "?"}`);
        else if (j.status === "canceled") toast(`Job ${j.kind} canceled`);
        // Whenever a job updates, refresh the runs list for the current
        // project so the new run row appears (and progress numbers update).
        if (state.selectedProject && j.project_id === state.selectedProject.id) {
          refreshRunsForProject();
          if (j.status === "done" || (j.done > 0 && j.done % 5 === 0)) {
            invalidateCacheForRun(j.run_id);
            if (state.selectedRun && state.selectedRun.id === j.run_id) renderMain();
          }
        }
      } catch (e) {}
    });
  }

  // ---------- boot ----------
  async function refreshRuns() {
    const fresh = await api.runs();
    state.runs = fresh;
    renderRunsList();
    if (state.selectedRun) {
      const updated = fresh.find(r => r.id === state.selectedRun.id);
      if (updated) state.selectedRun = updated;
    }
    if (!state.selectedRun && fresh.length > 0) selectRun(fresh[0].id);
  }

  // keyboard shortcuts
  document.addEventListener("keydown", (ev) => {
    if (ev.target.tagName === "INPUT" || ev.target.tagName === "TEXTAREA") return;
    if (ev.metaKey || ev.ctrlKey) return;
    const tabs = ["dashboard", "mentions", "sources", "playbook", "trend", "tools", "settings"];
    const idx = state.selectedRun ? state.runs.findIndex(r => r.id === state.selectedRun.id) : -1;
    if (ev.key === "j" && idx >= 0 && idx < state.runs.length - 1) selectRun(state.runs[idx + 1].id);
    if (ev.key === "k" && idx > 0) selectRun(state.runs[idx - 1].id);
    if (ev.key === "t") { toggleTheme(); return; }
    if (["1", "2", "3", "4", "5", "6", "7"].includes(ev.key)) {
      const t = tabs[Number(ev.key) - 1];
      if (t) switchView(t);
    }
  });

  // ---------- theme ----------
  const ICON_SUN = `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/>`;
  const ICON_MOON = `<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>`;

  function applyTheme(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    const icon = document.getElementById("themeIcon");
    const label = document.getElementById("themeLabel");
    if (icon && label) {
      icon.innerHTML = theme === "dark" ? ICON_SUN : ICON_MOON;
      label.textContent = theme === "dark" ? "Light mode" : "Dark mode";
    }
    try { localStorage.setItem("salience-theme", theme); } catch (e) {}
  }

  function toggleTheme() {
    const cur = document.documentElement.getAttribute("data-theme") || "dark";
    applyTheme(cur === "dark" ? "light" : "dark");
    // Chart colors are baked into SVG attributes at render time, so we
    // re-render the current view so they pick up the new palette.
    if (state.selectedRun) renderMain();
  }

  function bootTheme() {
    let saved = null;
    try { saved = localStorage.getItem("salience-theme"); } catch (e) {}
    if (saved === "light" || saved === "dark") {
      applyTheme(saved);
      return;
    }
    const prefersLight = window.matchMedia &&
      window.matchMedia("(prefers-color-scheme: light)").matches;
    applyTheme(prefersLight ? "light" : "dark");
  }

  // boot
  setupNav();
  bootTheme();
  document.getElementById("themeToggle").addEventListener("click", toggleTheme);

  // Project picker controls.
  document.getElementById("projectTrigger").addEventListener("click", (e) => {
    e.stopPropagation();
    $("#projectMenu").classList.toggle("hidden");
  });
  document.addEventListener("click", (e) => {
    if (!e.target.closest("#projectPicker")) closeProjectMenu();
  });
  document.getElementById("newProjectBtn").addEventListener("click", () => {
    closeProjectMenu();
    showProjectForm(null);
  });

  // Run-now button.
  document.getElementById("runNowBtn").addEventListener("click", showRunNowConfirm);

  // Modal close.
  document.getElementById("modalClose").addEventListener("click", closeModal);
  document.getElementById("modalBackdrop").addEventListener("click", (e) => {
    if (e.target.id === "modalBackdrop") closeModal();
  });

  // Initial load.
  (async () => {
    state.projects = await api.projects();
    state.selectedProject = state.projects[0] || null;
    renderProjectPicker();
    await refreshRunsForProject();
    updateRunNowEnabled();
    renderMain();
    startSSE();
  })();
})();
