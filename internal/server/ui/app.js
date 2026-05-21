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
    // Region filter for the Dashboard view. null = all regions aggregated.
    selectedRegion: null,
    // Flips true on first applyRegionAutoDetect() call so we never override
    // the user's explicit choice on subsequent project switches.
    regionAutoApplied: false,
    // UI language (decoupled from region). Initialized at boot from
    // localStorage / navigator.language. See detectLanguage().
    language: "en",
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
    // v0.4 "Answer Anatomy" endpoints.
    anatomy: (runId, sampleId) => fetch(`/api/runs/${runId}/anatomy/${sampleId}`).then(r => r.json()),
    domains: (runId) => fetch(`/api/runs/${runId}/domains`).then(r => r.json()),
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
      case "influence": return renderInfluenceView(main);
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
  // brandAppliesToRegion mirrors config.Brand.AppliesTo on the server.
  function brandAppliesToRegion(brand, regionCode) {
    if (!regionCode) return true;          // "All regions" view
    const regs = brand.regions || brand.Regions;
    if (!regs || regs.length === 0) return true;  // global brand
    return regs.indexOf(regionCode) >= 0;
  }

  // computeFilteredStats recalculates mention rates against only the cells
  // belonging to the selected region (or every cell when no region is
  // selected). Returns the shape downstream rendering needs.
  function computeFilteredStats(data, regionCode) {
    const cells = (data.Cells || []).filter(c =>
      !regionCode || c.Region === regionCode);
    // Aggregate brand mentions across the filtered cells.
    const totals = {};
    cells.forEach(c => {
      Object.keys(c.Rates || {}).forEach(brand => {
        if (!totals[brand]) totals[brand] = { sum: 0, n: 0 };
        totals[brand].sum += c.Rates[brand] * (c.Samples || 0);
        totals[brand].n   += (c.Samples || 0);
      });
    });
    const rates = {};
    Object.keys(totals).forEach(b => {
      rates[b] = totals[b].n > 0 ? totals[b].sum / totals[b].n : 0;
    });
    const samples = cells.reduce((s, c) => s + (c.Samples || 0), 0);
    // Competitor list filtered to those that apply to this region.
    const competitorBrands = (data.CompetitorBrands || data.Competitors || []).map(b =>
      typeof b === "string" ? { name: b } : b);
    const visibleCompetitors = competitorBrands
      .filter(b => brandAppliesToRegion(b, regionCode))
      .map(b => b.name || b.Name);
    return { cells, rates, samples, visibleCompetitors };
  }

  async function renderDashboard(main) {
    main.innerHTML = `<div class="loading">Loading…</div>`;
    const [data, trendPts] = await Promise.all([
      api.run(state.selectedRun.id),
      api.trend(),
    ]);

    // Available regions in this run, oldest first ("global" then alphabetical).
    const regionSet = new Set();
    (data.Cells || []).forEach(c => regionSet.add(c.Region || "global"));
    const availableRegions = Array.from(regionSet).sort((a, b) => {
      if (a === "global") return -1;
      if (b === "global") return 1;
      return a.localeCompare(b);
    });
    // If only one region in the run, no need for a filter — implicitly null.
    const showRegionPicker = availableRegions.length > 1;
    // Default state.selectedRegion to null = "All regions"
    // Recompute everything for the active filter.
    const filtered = computeFilteredStats(data, state.selectedRegion);

    // ----- compute hero stats from filtered data -----
    const yourRate = filtered.rates[data.UserBrand] || 0;
    const compRates = filtered.visibleCompetitors
      .map(c => ({ name: c, rate: filtered.rates[c] || 0 }))
      .sort((a, b) => b.rate - a.rate);
    const topComp = compRates[0] || { name: "—", rate: 0 };
    const losingCount = filtered.cells.filter(c => c.Gap < -0.005).length;
    const totalCount  = filtered.cells.length;

    // brand sparkline across runs
    const userSpark = trendPts.map(p => p.rates[data.UserBrand] || 0);
    const trendDelta = userSpark.length >= 2
      ? userSpark[userSpark.length - 1] - userSpark[userSpark.length - 2]
      : 0;

    // The region+language picker now lives top-right (see updateRegionPicker
    // below). Keep showRegionPicker for the table-column logic and refresh
    // the picker's menu so newly-loaded runs surface their regions.
    void showRegionPicker;
    updateRegionPicker(availableRegions, data);

    const regionContext = state.selectedRegion
      ? ` in <strong>${escape(regionLabelOf(data, state.selectedRegion))}</strong>`
      : "";

    // Hero summary in plain English. Avoids "LLM", "prompt", "sample" —
    // the marketing-manager test: would this sentence read fine to someone
    // who has never used the AI ecosystem?
    const hero = `
      <div class="hero">
        <div>
          <h1 class="hero-title">${escape(data.UserBrand)}</h1>
          <p class="hero-sub">
            AI mentioned your brand in <strong>${pct(yourRate)}</strong> of answers${regionContext},
            out of <strong>${filtered.samples}</strong> we tested.
            ${topComp.rate > yourRate
              ? `Your biggest competitor <strong>${escape(topComp.name)}</strong> beat you with <strong class="bad">${pct(topComp.rate)}</strong>.`
              : `You're leading or tied with every competitor we track.`}
            You're losing on <strong class="${losingCount > 0 ? "bad" : "good"}">${losingCount} of ${totalCount}</strong> questions.
          </p>
        </div>
        <div class="hero-meta">
          <div><b>Test #${data.RunID}</b></div>
          <div>${escape(data.Started)}</div>
          <div>${escape(data.Status)}</div>
        </div>
      </div>
    `;

    // ----- top stat cards -----
    const stats = `
      <div class="stat-grid">
        <div class="stat accent">
          <div class="stat-label">${t("your_mention_rate")}</div>
          <div class="stat-value">${pct(yourRate)}</div>
          <div class="stat-sub">
            ${trendDelta === 0 ? '<span class="muted">no change vs prev run</span>'
              : trendDelta > 0 ? `<span class="delta-up">▲ ${pct(Math.abs(trendDelta))}</span> <span class="muted">vs prev run</span>`
              : `<span class="delta-down">▼ ${pct(Math.abs(trendDelta))}</span> <span class="muted">vs prev run</span>`}
          </div>
          ${userSpark.length > 1 ? sparkline(userSpark, 90, 24) : ""}
        </div>
        <div class="stat">
          <div class="stat-label">${t("top_competitor")}</div>
          <div class="stat-value">${pct(topComp.rate)}</div>
          <div class="stat-sub muted">${escape(topComp.name)}</div>
        </div>
        <div class="stat">
          <div class="stat-label">${t("prompts_losing")}</div>
          <div class="stat-value">${losingCount}<span class="muted" style="font-size:18px"> / ${totalCount}</span></div>
          <div class="stat-sub muted">${totalCount - losingCount} won or tied</div>
        </div>
        <div class="stat">
          <div class="stat-label">${t("total_samples")}</div>
          <div class="stat-value">${data.TotalSamples}</div>
          <div class="stat-sub muted">${data.TotalFailures} failed</div>
        </div>
      </div>
    `;

    // ----- brand-bar chart -----
    const allBrands = [{ name: data.UserBrand, rate: yourRate, you: true }]
      .concat(compRates.map(c => ({ name: c.name, rate: c.rate })));
    const brandBars = `
      <div class="card">
        <div class="card-head">
          <h3>${t("mention_rate_by_brand")}</h3>
          <span class="card-hint">${escape(t("across_all_questions"))}</span>
        </div>
        <div class="help">
          <b>What this shows:</b> for every question we tested, how often each brand showed up
          in AI's answer (either in the text or in a website it quoted). 100% means AI mentioned
          that brand every single time.
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

    // ----- sentiment donut (filtered by selected region too) -----
    const sentMentions = await api.mentions(state.selectedRun.id);
    const filteredSampleIds = new Set(filtered.cells.flatMap(c => c.sampleIds || []));
    // No per-cell sample id list is exposed today, so when region filter is
    // active we fall back to filtering mentions by sample's region via
    // /api/runs/N/mentions which doesn't yet expose Region. To stay
    // accurate, scope by region: every sample's prompt is in the cell list.
    const cellPrompts = new Set(filtered.cells.map(c => c.Prompt));
    const sentCounts = { positive: 0, neutral: 0, negative: 0 };
    sentMentions.forEach(m => {
      if (m.Brand !== data.UserBrand) return;
      // If a region filter is active, restrict to mentions whose underlying
      // sample's prompt belongs to a filtered cell. (When no filter is
      // active cellPrompts contains every prompt, so the predicate is a
      // no-op.)
      if (state.selectedRegion && !cellPrompts.has(m.Prompt)) return;
      const s = m.Sentiment || "neutral";
      sentCounts[s] = (sentCounts[s] || 0) + 1;
    });
    void filteredSampleIds;
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
          <h3>${t("your_sentiment")}</h3>
          <span class="card-hint">${escape(t("how_ai_talks"))}</span>
        </div>
        <div class="help">
          <b>Why this matters:</b> being mentioned all the time isn't great if AI is warning people
          against you. We score the sentence around each mention as positive, neutral, or negative.
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

    // ----- prompts table (filtered cells + per-region competitor columns) -----
    // The competitor columns shown are exactly the ones relevant to the
    // active region filter. When viewing "All regions" we show every
    // competitor that's relevant to at least one region in the run.
    const cells = filtered.cells.slice().sort((a, b) => a.Gap - b.Gap);
    const compHeaderCols = filtered.visibleCompetitors;
    const compHead = compHeaderCols.map(c => `<th>${escape(c)}</th>`).join("");
    const showRegionCol = !state.selectedRegion && availableRegions.length > 1;
    const rows = cells.map(c => {
      const klass = c.Gap < -0.005 ? "behind" : c.Gap > 0.005 ? "ahead" : "";
      const lowN = c.Samples < 10 ? `<span class="low-n" title="Tested fewer than 10 times — numbers are rough">!</span>` : "";
      const userR = c.Rates[data.UserBrand] || 0;
      const lo = (c.CILow && c.CILow[data.UserBrand]) || 0;
      const hi = (c.CIHigh && c.CIHigh[data.UserBrand]) || 0;
      // The Anatomy button opens the modal against the first underlying
      // sample id (any sample in the bucket is representative). Cells
      // without SampleIDs (e.g. all-errored buckets) get a disabled link.
      const sid = (c.SampleIDs && c.SampleIDs[0]) || 0;
      const anatomyBtn = sid
        ? `<button class="row-anatomy" data-sample-id="${sid}" title="${escape(t("open_anatomy"))}">
             <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2">
               <circle cx="11" cy="11" r="6"/><path d="M21 21l-4.35-4.35"/>
             </svg>
           </button>`
        : `<span class="muted" style="font-size:11px">—</span>`;
      return `<tr>
        <td class="prompt" title="${escape(c.Prompt)}">${escape(clip(c.Prompt, 60))}</td>
        <td class="muted">${escape(c.ProviderName)}</td>
        ${showRegionCol ? `<td class="muted">${escape(regionLabelOf(data, c.Region))}</td>` : ""}
        <td class="num">${c.Samples}${lowN}</td>
        <td class="num">${pct(userR)}<span class="ci">${pct(lo)}–${pct(hi)}</span></td>
        ${compHeaderCols.map(comp => `<td class="num">${pct(c.Rates[comp] || 0)}</td>`).join("")}
        <td class="num gap ${klass}">${signedPct(c.Gap)}</td>
        <td class="anatomy-cell">${anatomyBtn}</td>
      </tr>`;
    }).join("");

    const lowAny = cells.some(c => c.Samples < 10);
    const promptTable = `
      <div class="card">
        <div class="card-head">
          <h3>${t("per_prompt_detail")}</h3>
          <span class="card-hint">${escape(t("sort_worst_first"))}</span>
        </div>
        ${lowAny ? `<div class="help">
          <b>⚠ Heads up:</b> some rows tested AI fewer than 10 times, so the percentages are rough.
          The small numbers next to the percentage show the realistic range. Test more times for sharper numbers.
        </div>` : ""}
        <table class="prompts">
          <thead>
            <tr>
              <th>${escape(t("col_question"))}</th><th>${escape(t("col_ai"))}</th>
              ${showRegionCol ? `<th>${escape(t("col_region"))}</th>` : ""}
              <th>${escape(t("col_tests"))}</th>
              <th>${escape(data.UserBrand)}</th>
              ${compHead}
              <th>${escape(t("col_gap"))}</th>
              <th aria-label="${escape(t("open_anatomy"))}"></th>
            </tr>
          </thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;

    main.innerHTML = hero + stats +
      `<div class="cards-grid">${brandBars}${sentimentCard}</div>` +
      promptTable;

    // Wire each row's anatomy button after the table is in the DOM.
    main.querySelectorAll(".row-anatomy").forEach(btn => {
      btn.addEventListener("click", (e) => {
        e.stopPropagation();
        const id = parseInt(btn.dataset.sampleId, 10);
        if (id > 0) openAnatomyModal(state.selectedRun.id, id);
      });
    });
  }

  // ---------- top-right region picker ----------
  // Rendered once on boot, then updated each time a project or run is
  // selected so the menu reflects the regions actually available in the
  // active run.

  function updateRegionPicker(availableRegions, data) {
    const wrap = document.getElementById("regionPickerFixed");
    if (!wrap) return;

    // Hide the picker entirely for single-region runs — it would be a
    // permanent no-op there.
    if (!availableRegions || availableRegions.length <= 1) {
      wrap.classList.add("hidden");
      return;
    }
    wrap.classList.remove("hidden");

    const cur = regionDisplay(state.selectedRegion);
    document.getElementById("regionToggleFlag").textContent  = cur.flag;
    document.getElementById("regionToggleLabel").textContent = cur.label;
    // Localize the "Market" caption above the current region.
    const cap = document.querySelector("#regionPickerFixed .region-label-caption");
    if (cap) cap.textContent = t("market");

    // Build menu entries: "All regions" + each region in the run.
    const items = [
      { code: "", label: t("all_regions"), flag: "🌐", native: "" },
      null, // divider
      ...availableRegions.map(code => {
        const d = regionDisplay(code);
        // Prefer the project-defined label if it differs from the preset.
        let label = d.label;
        if (state.selectedProject && state.selectedProject.regions) {
          const pr = state.selectedProject.regions.find(r => r.code === code);
          if (pr && pr.label) label = pr.label;
        }
        return { code, label, flag: d.flag, native: "" };
      }),
    ];

    const menu = document.getElementById("regionMenu");
    menu.innerHTML = items.map(item => {
      if (item === null) return `<div class="region-menu-divider"></div>`;
      const active = (state.selectedRegion || "") === item.code ? "active" : "";
      return `
        <div class="region-menu-item ${active}" data-region="${escape(item.code)}">
          <span class="flag">${item.flag}</span>
          <div class="text">
            <span class="name">${escape(item.label)}</span>
          </div>
        </div>
      `;
    }).join("");

    // Click handlers.
    menu.querySelectorAll(".region-menu-item").forEach(el => {
      el.addEventListener("click", () => {
        const code = el.dataset.region;
        state.selectedRegion = code || null;
        // Persist the explicit choice so future sessions skip auto-detect.
        // "__all__" is the sentinel for "user explicitly picked All regions"
        // — distinct from "no choice yet" which is the absent key.
        localStorage.setItem("salience-region", code || "__all__");
        state.regionAutoApplied = true; // user has now overridden
        menu.classList.add("hidden");
        // Re-render the active view so all panels reflect the new filter.
        renderMain();
      });
    });
  }

  function setupRegionPicker() {
    const btn = document.getElementById("regionToggleBtn");
    const menu = document.getElementById("regionMenu");
    if (!btn || !menu) return;
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      // Close the language menu if it happens to be open — only one dropdown
      // visible at a time keeps the UI tidy.
      document.getElementById("languageMenu")?.classList.add("hidden");
      menu.classList.toggle("hidden");
    });
    document.addEventListener("click", (e) => {
      if (!e.target.closest("#regionPickerFixed")) menu.classList.add("hidden");
    });
  }

  // ---------- UI language picker (decoupled from region) ----------
  // Region answers "which market is this data about?". Language answers
  // "which language should the UI itself be in?". A Japanese user studying
  // the US market can read the dashboard in Japanese; an American studying
  // Japan can read it in English. So they are independent controls.
  //
  // LANGUAGES is the catalog of UI translations we currently ship. Adding
  // a new language means adding an entry here plus a translation map in
  // TRANSLATIONS. The catalog is intentionally kept small (the most-asked
  // for at launch); user-contributed maps can be added incrementally.
  const LANGUAGES = [
    { code: "en", label: "English",   flag: "🇬🇧", native: "English"    },
    { code: "ja", label: "Japanese",  flag: "🇯🇵", native: "日本語"     },
    { code: "hi", label: "Hindi",     flag: "🇮🇳", native: "हिन्दी"     },
    { code: "es", label: "Spanish",   flag: "🇪🇸", native: "Español"   },
    { code: "de", label: "German",    flag: "🇩🇪", native: "Deutsch"   },
    { code: "fr", label: "French",    flag: "🇫🇷", native: "Français"  },
    { code: "pt", label: "Portuguese",flag: "🇧🇷", native: "Português" },
    { code: "ko", label: "Korean",    flag: "🇰🇷", native: "한국어"     },
  ];

  // TRANSLATIONS[lang][key] — minimal pack covering the most-visible UI
  // strings. Any key missing from a non-English map silently falls back to
  // the English string so the UI never shows a raw key.
  const TRANSLATIONS = {
    en: {
      market: "Market", language: "Language",
      all_regions: "All regions",
      dashboard: "Dashboard",
      mentions: "Mentions",
      sources: "Sources",
      // "Influence" tab → plainer label so non-techies get the point.
      influence: "Where AI looks",
      playbook: "Action plan",
      trend: "Over time",
      tools: "Tools", settings: "Settings",
      run_benchmark: "Test now",
      recent_runs: "Recent tests",
      theme: "Theme",
      // Stat cards: avoid "mention rate", "samples", "prompts" — these are
      // jargon to most users. Marketing folks understand "shows up", "AI
      // answer", "questions" much faster.
      your_mention_rate: "How often AI mentions you",
      top_competitor: "Biggest competitor",
      prompts_losing: "Questions you're losing",
      total_samples: "AI answers checked",
      mention_rate_by_brand: "Who AI mentions most",
      your_sentiment: "How AI describes you",
      per_prompt_detail: "Question-by-question breakdown",
      anatomy: "Why this answer?",
      open_anatomy: "See why",
      domain_hit_list: "Websites AI quotes from",
      domain_hit_list_hint: "The sites AI reads when answering — and whether you're on them",
      // Modal section heads.
      anatomy_step_prompt: "What we asked AI",
      anatomy_step_tools: "What AI searched for",
      anatomy_step_sources: "Websites AI read",
      anatomy_step_answer: "What AI said back",
      anatomy_step_diagnosis: "What to do about it",
      // Plain language for everything else
      sort_worst_first: "Worst gaps first",
      across_all_questions: "Across every question we tested",
      how_ai_talks: "Tone AI uses when describing you",
      sorted_by_cites: "Most-cited first",
      total_citations: "Times AI quoted a website",
      top_website: "Most-quoted website",
      websites_youre_on: "Websites where you appear",
      ai_searched_web: "AI searched the web",
      ai_didnt_share_query: "AI searched but didn't share what it typed",
      no_tools_captured: "AI didn't search anywhere for this one",
      no_sources_cited: "AI didn't quote any websites",
      no_findings: "Nothing notable to flag for this answer",
      you_on_page: "you appear on this page",
      you_not_on_page: "you're not on this page",
      profile_pending: "Still loading details — check back in a moment",
      crawl_error: "Couldn't read this page",
      words: "words",
      trust_score: "Trust",
      // Page kind labels (mirror humanizeKind values in plain English).
      kind_review_aggregator: "Review site",
      kind_brand_own: "Their own site",
      kind_encyclopedia: "Encyclopedia",
      kind_listicle: "'Best of' article",
      kind_news: "News article",
      kind_other: "Other",
      // Severity badges.
      sev_critical: "fix this",
      sev_warn: "worth a look",
      sev_info: "FYI",
      // Column headers.
      col_question: "Question",
      col_ai: "AI",
      col_tests: "Tests",
      col_gap: "Gap",
      col_region: "Market",
      col_cites: "Times quoted",
      col_website: "Website",
      col_you: "You",
      col_competitors: "Competitors",
      col_trust: "Trust",
      you_present: "you're on this site",
      you_absent: "not listed",
    },
    ja: {
      market: "市場", language: "言語",
      all_regions: "全地域",
      dashboard: "ダッシュボード",
      mentions: "言及一覧",
      sources: "情報源",
      influence: "AI が参照するサイト",
      playbook: "やることリスト",
      trend: "推移",
      tools: "ツール", settings: "設定",
      run_benchmark: "テスト実行",
      recent_runs: "最近のテスト",
      theme: "テーマ",
      your_mention_rate: "AI に名前を出される率",
      top_competitor: "最大の競合",
      prompts_losing: "負けている質問",
      total_samples: "確認した AI 回答数",
      mention_rate_by_brand: "AI が最もよく出すブランド",
      your_sentiment: "AI による自社の語り方",
      per_prompt_detail: "質問別の内訳",
      anatomy: "なぜこの答え?",
      open_anatomy: "理由を見る",
      domain_hit_list: "AI が引用するサイト",
      domain_hit_list_hint: "AI が答える時に読むサイトと、自社の掲載状況",
      anatomy_step_prompt: "AI に聞いたこと",
      anatomy_step_tools: "AI が検索したキーワード",
      anatomy_step_sources: "AI が読んだサイト",
      anatomy_step_answer: "AI の答え",
      anatomy_step_diagnosis: "やるべきこと",
      sort_worst_first: "差が大きい順",
      across_all_questions: "テストした全質問にわたって",
      how_ai_talks: "AI があなたを語る時のトーン",
      sorted_by_cites: "引用回数の多い順",
      total_citations: "AI がサイトを引用した回数",
      top_website: "最も引用されたサイト",
      websites_youre_on: "あなたが掲載されているサイト",
      ai_searched_web: "AI がウェブを検索しました",
      ai_didnt_share_query: "検索はしたが、内容は不明",
      no_tools_captured: "この回答では検索しませんでした",
      no_sources_cited: "この回答ではサイトを引用しませんでした",
      no_findings: "特に指摘する点はありません",
      you_on_page: "このページに掲載あり",
      you_not_on_page: "このページに掲載なし",
      profile_pending: "詳細を読み込み中 — 少しお待ちください",
      crawl_error: "このページは読み込めませんでした",
      words: "語",
      trust_score: "信頼度",
      kind_review_aggregator: "レビューサイト",
      kind_brand_own: "ブランド自社サイト",
      kind_encyclopedia: "百科事典",
      kind_listicle: "「おすすめ」記事",
      kind_news: "ニュース記事",
      kind_other: "その他",
      sev_critical: "要対応",
      sev_warn: "注意",
      sev_info: "参考",
      col_question: "質問",
      col_ai: "AI",
      col_tests: "回数",
      col_gap: "差",
      col_region: "市場",
      col_cites: "引用回数",
      col_website: "サイト",
      col_you: "自社",
      col_competitors: "競合",
      col_trust: "信頼度",
      you_present: "掲載あり",
      you_absent: "掲載なし",
    },
    hi: {
      market: "बाज़ार", language: "भाषा",
      all_regions: "सभी क्षेत्र",
      dashboard: "डैशबोर्ड", mentions: "उल्लेख", sources: "स्रोत",
      playbook: "प्लेबुक", trend: "रुझान", tools: "उपकरण", settings: "सेटिंग्स",
      run_benchmark: "बेंचमार्क चलाएँ", recent_runs: "हाल के रन",
      theme: "थीम",
      your_mention_rate: "आपका उल्लेख दर",
      top_competitor: "शीर्ष प्रतिस्पर्धी",
      prompts_losing: "हारते हुए प्रॉम्प्ट",
      total_samples: "कुल सैम्पल",
      mention_rate_by_brand: "ब्रांड के अनुसार उल्लेख दर",
      your_sentiment: "आपकी भावना",
      per_prompt_detail: "प्रति प्रॉम्प्ट विवरण",
    },
    es: {
      market: "Mercado", language: "Idioma",
      all_regions: "Todas las regiones",
      dashboard: "Panel", mentions: "Menciones", sources: "Fuentes",
      playbook: "Guía", trend: "Tendencia", tools: "Herramientas", settings: "Ajustes",
      run_benchmark: "Ejecutar benchmark", recent_runs: "Ejecuciones recientes",
      theme: "Tema",
      your_mention_rate: "Tu tasa de mención",
      top_competitor: "Competidor principal",
      prompts_losing: "Prompts perdiendo",
      total_samples: "Muestras totales",
      mention_rate_by_brand: "Tasa de mención por marca",
      your_sentiment: "Tu sentimiento",
      per_prompt_detail: "Detalle por prompt",
    },
    de: {
      market: "Markt", language: "Sprache",
      all_regions: "Alle Regionen",
      dashboard: "Übersicht", mentions: "Erwähnungen", sources: "Quellen",
      playbook: "Playbook", trend: "Verlauf", tools: "Werkzeuge", settings: "Einstellungen",
      run_benchmark: "Benchmark starten", recent_runs: "Letzte Läufe",
      theme: "Design",
    },
    fr: {
      market: "Marché", language: "Langue",
      all_regions: "Toutes les régions",
      dashboard: "Tableau de bord", mentions: "Mentions", sources: "Sources",
      playbook: "Playbook", trend: "Tendance", tools: "Outils", settings: "Réglages",
      run_benchmark: "Lancer un benchmark", recent_runs: "Exécutions récentes",
      theme: "Thème",
    },
    pt: {
      market: "Mercado", language: "Idioma",
      all_regions: "Todas as regiões",
      dashboard: "Painel", mentions: "Menções", sources: "Fontes",
      playbook: "Manual", trend: "Tendência", tools: "Ferramentas", settings: "Configurações",
      run_benchmark: "Executar benchmark", recent_runs: "Execuções recentes",
      theme: "Tema",
    },
    ko: {
      market: "시장", language: "언어",
      all_regions: "전체 지역",
      dashboard: "대시보드", mentions: "언급", sources: "출처",
      playbook: "플레이북", trend: "추세", tools: "도구", settings: "설정",
      run_benchmark: "벤치마크 실행", recent_runs: "최근 실행",
      theme: "테마",
    },
  };

  // t(key) — return the translated string for the active UI language. Falls
  // back to the English entry, then to the raw key, so the UI degrades
  // gracefully when a translation is missing rather than rendering blanks.
  function t(key) {
    const lang = state.language || "en";
    const pack = TRANSLATIONS[lang] || {};
    if (pack[key] !== undefined) return pack[key];
    if (TRANSLATIONS.en[key] !== undefined) return TRANSLATIONS.en[key];
    return key;
  }

  // Pick a sensible default language on first load. We honour an explicit
  // user choice from localStorage; otherwise we sniff navigator.language so
  // a Japanese browser opens in Japanese. Falls back to English.
  function detectLanguage() {
    const saved = localStorage.getItem("salience-lang");
    if (saved && TRANSLATIONS[saved]) return saved;
    const nav = (navigator.language || "en").toLowerCase();
    for (const lang of LANGUAGES) {
      if (nav === lang.code || nav.startsWith(lang.code + "-")) return lang.code;
    }
    return "en";
  }

  // TZ_TO_REGION maps the most common IANA timezones to our short region
  // codes. Timezone is a more reliable physical-location signal than the
  // navigator locale (a Japanese-speaking user living in California will
  // still report "America/Los_Angeles"). We only enumerate the zones for
  // the regions REGION_PRESETS knows about — everything else falls back
  // to the locale heuristic.
  const TZ_TO_REGION = {
    "Asia/Tokyo":           "jp",
    "Asia/Seoul":           "kr",
    "Asia/Kolkata":         "in",
    "Asia/Calcutta":        "in",
    "Asia/Jakarta":         "id",
    "Asia/Pontianak":       "id",
    "Asia/Makassar":        "id",
    "Asia/Jayapura":        "id",
    "Europe/London":        "uk",
    "Europe/Belfast":       "uk",
    "Europe/Berlin":        "de",
    "Europe/Munich":        "de",
    "Europe/Paris":         "fr",
    "America/New_York":     "us",
    "America/Chicago":      "us",
    "America/Denver":       "us",
    "America/Phoenix":      "us",
    "America/Los_Angeles":  "us",
    "America/Anchorage":    "us",
    "Pacific/Honolulu":     "us",
    "America/Mexico_City":  "mx",
    "America/Tijuana":      "mx",
    "America/Monterrey":    "mx",
    "America/Cancun":       "mx",
    "America/Sao_Paulo":    "br",
    "America/Bahia":        "br",
    "America/Fortaleza":    "br",
    "America/Manaus":       "br",
    "America/Recife":       "br",
  };

  // detectRegionFromBrowser returns the best guess at the user's market
  // region. Priority: explicit localStorage choice → IANA timezone →
  // locale country suffix → null (= All regions). Returns a code that
  // *may or may not* exist in the active project's region list — the
  // caller filters that out.
  function detectRegionFromBrowser() {
    const saved = localStorage.getItem("salience-region");
    if (saved !== null) return saved === "__all__" ? null : saved;

    // Timezone is the most reliable physical-location signal browsers
    // expose without permission prompts. A null/missing TZ falls through.
    try {
      const tz = (Intl.DateTimeFormat().resolvedOptions().timeZone) || "";
      if (TZ_TO_REGION[tz]) return TZ_TO_REGION[tz];
    } catch (e) { /* fall through to locale heuristic */ }

    // Locale fallback: navigator.language like "ja-JP" / "en-US".
    const nav = (navigator.language || "").toLowerCase();
    const m = nav.match(/-([a-z]{2})$/);
    if (m) {
      const cc = m[1];
      if (REGION_PRESETS.find(r => r.code === cc)) return cc;
    }
    return null;
  }

  // applyRegionAutoDetect runs once per session and seeds state.selectedRegion
  // from the browser if (a) the user hasn't explicitly picked a region this
  // session AND (b) the auto-detected region is actually configured on the
  // currently-active project. Otherwise we leave selectedRegion alone — a
  // bad auto-pick is worse than no auto-pick.
  function applyRegionAutoDetect() {
    if (state.regionAutoApplied) return;
    state.regionAutoApplied = true;
    const detected = detectRegionFromBrowser();
    if (detected === null) return; // already "All regions"
    const projRegions = (state.selectedProject && state.selectedProject.regions) || [];
    if (projRegions.some(r => r.code === detected)) {
      state.selectedRegion = detected;
    }
  }

  // Rebuild the language picker label + menu. Called once at boot and after
  // every selection so the menu shows the current "active" highlight.
  function updateLanguagePicker() {
    const cur = LANGUAGES.find(l => l.code === state.language) || LANGUAGES[0];
    document.getElementById("languageToggleFlag").textContent  = cur.flag;
    document.getElementById("languageToggleLabel").textContent = cur.native;
    const cap = document.querySelector("#languagePickerFixed .region-label-caption");
    if (cap) cap.textContent = t("language");

    const menu = document.getElementById("languageMenu");
    menu.innerHTML = LANGUAGES.map(l => {
      const active = l.code === state.language ? "active" : "";
      return `
        <div class="region-menu-item ${active}" data-language="${escape(l.code)}">
          <span class="flag">${l.flag}</span>
          <div class="text">
            <span class="name">${escape(l.native)}</span>
            <span class="native">${escape(l.label)}</span>
          </div>
        </div>
      `;
    }).join("");

    menu.querySelectorAll(".region-menu-item").forEach(el => {
      el.addEventListener("click", () => {
        const code = el.dataset.language;
        state.language = code;
        localStorage.setItem("salience-lang", code);
        menu.classList.add("hidden");
        applyStaticTranslations(); // re-translate sidebar + chrome
        renderMain();              // re-render the active view in the new language
        updateLanguagePicker();    // refresh the menu's "active" highlight
      });
    });
  }

  function setupLanguagePicker() {
    const btn = document.getElementById("languageToggleBtn");
    const menu = document.getElementById("languageMenu");
    if (!btn || !menu) return;
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      document.getElementById("regionMenu")?.classList.add("hidden");
      menu.classList.toggle("hidden");
    });
    document.addEventListener("click", (e) => {
      if (!e.target.closest("#languagePickerFixed")) menu.classList.add("hidden");
    });
  }

  // Re-translate the static sidebar labels (nav tabs, run-benchmark button,
  // recent-runs heading, theme toggle). These live outside of renderMain's
  // re-render scope, so they need their own pass on every language change.
  function applyStaticTranslations() {
    const navMap = {
      dashboard: t("dashboard"),
      mentions:  t("mentions"),
      sources:   t("sources"),
      influence: t("influence"),
      playbook:  t("playbook"),
      trend:     t("trend"),
      tools:     t("tools"),
      settings:  t("settings"),
    };
    document.querySelectorAll(".tab-link[data-view]").forEach(el => {
      const view = el.dataset.view;
      const label = navMap[view];
      if (!label) return;
      // Preserve the leading svg icon; only the trailing text node is replaced.
      const last = el.lastChild;
      if (last && last.nodeType === Node.TEXT_NODE) {
        last.nodeValue = " " + label;
      }
    });
    const runBtn = document.getElementById("runNowBtn");
    if (runBtn) {
      const last = runBtn.lastChild;
      if (last && last.nodeType === Node.TEXT_NODE) last.nodeValue = " " + t("run_benchmark");
    }
    const sectionLabel = document.querySelector(".runs-block .section-label");
    if (sectionLabel) sectionLabel.textContent = t("recent_runs");
    const themeLabel = document.getElementById("themeLabel");
    if (themeLabel) themeLabel.textContent = t("theme");
  }

  // regionLabelOf returns the full human label for a region code, falling
  // back to the picker presets when the project's own region list doesn't
  // carry a label. Always returns a full name (e.g. "Japan"), never the
  // short code "jp".
  function regionLabelOf(data, code) {
    if (!code || code === "global") return "Global";
    const proj = state.selectedProject;
    if (proj && proj.regions) {
      for (const r of proj.regions) {
        if (r.code === code && r.label) return r.label;
      }
    }
    return regionDisplay(code).label;
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

  // ---------- Influence view (v0.4) ----------
  // Domain hit list: which domains the LLMs in this run cite most, who's
  // on them, who isn't. The single most actionable artifact in the app —
  // it tells the user exactly which sites to invest in.
  async function renderInfluenceView(main) {
    main.innerHTML = `<div class="loading">Loading influence…</div>`;
    const data = await api.domains(state.selectedRun.id);
    if (!data || data.length === 0) {
      main.innerHTML = `<div class="empty">
        <div class="icon">
          <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.6">
            <circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a15 15 0 0 1 0 18M12 3a15 15 0 0 0 0 18"/>
          </svg>
        </div>
        <h2>No websites quoted yet</h2>
        <p>We're still reading the sites AI quoted. Check back in a minute, or try testing again if this run had no sources.</p>
      </div>`;
      return;
    }

    // Hero stats — how many websites and how many your brand is on.
    const total = data.length;
    const present = data.filter(d => d.user_present).length;
    const totalCitations = data.reduce((s, d) => s + d.citations, 0);
    const hero = `
      <div class="hero">
        <div class="hero-eyebrow">${escape(t("influence"))}</div>
        <h1 class="hero-title">${escape(t("domain_hit_list"))}</h1>
        <p class="hero-sub">${escape(t("domain_hit_list_hint"))}</p>
      </div>
      <div class="stat-grid">
        <div class="stat accent">
          <div class="stat-label">${escape(t("websites_youre_on"))}</div>
          <div class="stat-value">${present}<span class="muted" style="font-size:18px"> / ${total}</span></div>
          <div class="stat-sub muted">of the sites AI quoted</div>
        </div>
        <div class="stat">
          <div class="stat-label">${escape(t("total_citations"))}</div>
          <div class="stat-value">${totalCitations}</div>
          <div class="stat-sub muted">across every answer</div>
        </div>
        <div class="stat">
          <div class="stat-label">${escape(t("top_website"))}</div>
          <div class="stat-value">${data[0].citations}<span class="muted" style="font-size:18px"> times</span></div>
          <div class="stat-sub muted">${escape(data[0].domain)}</div>
        </div>
      </div>
    `;

    // The list itself.
    const rows = data.map(d => {
      const kindBadge = d.page_kind ? `<span class="badge kind-${escape(d.page_kind)}">${escape(humanizeKind(d.page_kind))}</span>` : "";
      const compsCol = d.competitor_count > 0
        ? `<span class="comp-pills">${(d.competitor_names || []).slice(0, 3).map(c => `<span class="comp-pill">${escape(c)}</span>`).join("")}${d.competitor_count > 3 ? `<span class="comp-more">+${d.competitor_count - 3}</span>` : ""}</span>`
        : `<span class="muted">—</span>`;
      const youCol = d.user_present
        ? `<span class="you-badge present">✓ ${escape(t("you_present"))}</span>`
        : `<span class="you-badge absent">${escape(t("you_absent"))}</span>`;
      const url = (d.sample_urls && d.sample_urls[0]) || "";
      const authBar = d.authority_score
        ? `<div class="auth-bar" title="${escape(t("trust_score"))}: ${d.authority_score}/100"><div class="auth-fill" style="width:${d.authority_score}%"></div></div>`
        : `<span class="muted">—</span>`;
      return `<tr>
        <td><span class="cite-count">${d.citations}</span></td>
        <td class="domain-cell">
          <a href="${escape(url)}" target="_blank" rel="noopener" class="domain-link">${escape(d.domain)}</a>
          ${kindBadge}
        </td>
        <td>${authBar}</td>
        <td>${youCol}</td>
        <td>${compsCol}</td>
      </tr>`;
    }).join("");

    const tableCard = `
      <div class="card">
        <div class="card-head">
          <h3>${escape(t("domain_hit_list"))}</h3>
          <span class="card-hint">${escape(t("sorted_by_cites"))}</span>
        </div>
        <div class="help">
          <b>How to use this:</b> AI quoted each of these websites at least once while answering. Sites where you're <span class="you-badge absent">${escape(t("you_absent"))}</span> are your biggest opportunities — getting your brand listed there is what will change AI's answers next time.
        </div>
        <table class="prompts">
          <thead><tr>
            <th>${escape(t("col_cites"))}</th>
            <th>${escape(t("col_website"))}</th>
            <th>${escape(t("col_trust"))}</th>
            <th>${escape(t("col_you"))}</th>
            <th>${escape(t("col_competitors"))}</th>
          </tr></thead>
          <tbody>${rows}</tbody>
        </table>
      </div>
    `;
    main.innerHTML = hero + tableCard;
  }

  // humanizeKind turns the server's PageKind enum into a short label
  // the user can read. Translation-aware: uses t() so it switches with
  // the language picker. Kept close to renderInfluenceView so the two
  // change together when we add a kind.
  function humanizeKind(k) {
    switch (k) {
      case "review_aggregator": return t("kind_review_aggregator");
      case "brand_own":         return t("kind_brand_own");
      case "encyclopedia":      return t("kind_encyclopedia");
      case "listicle":          return t("kind_listicle");
      case "news":              return t("kind_news");
      default:                  return t("kind_other");
    }
  }

  // ---------- Anatomy modal (v0.4) ----------
  // Opened from the per-prompt table or an Influence row. Shows the
  // complete chain: prompt sent → tool calls → cited sources (with
  // page-side analysis) → raw answer → diagnoses.
  async function openAnatomyModal(runId, sampleId) {
    openModal(t("anatomy"), `<div class="loading">Loading anatomy…</div>`, { large: true });
    let a;
    try {
      a = await api.anatomy(runId, sampleId);
    } catch (e) {
      $("#modalBody").innerHTML = `<div class="empty">Couldn't load this answer: ${escape(String(e))}</div>`;
      return;
    }

    // What AI searched for. Synthesised "(inferred)" entries get a
    // gentler phrasing so non-techies don't see "tool call" jargon.
    const toolCallsBlock = (a.tool_calls && a.tool_calls.length > 0)
      ? a.tool_calls.map(tc => {
          // Detect the synthesised "we don't know what it searched" entry
          // and replace the cryptic note with a plain explanation.
          const query = tc.query || "";
          const wasInferred = query.startsWith("(inferred");
          const displayQuery = wasInferred ? t("ai_didnt_share_query") : query;
          const label = (tc.kind === "web_search") ? t("ai_searched_web") : tc.kind;
          return `
            <div class="toolcall">
              <span class="toolcall-kind">${escape(label)}</span>
              <span class="toolcall-query ${wasInferred ? "muted" : ""}">${escape(displayQuery)}</span>
              ${tc.result_count ? `<span class="toolcall-count">→ ${tc.result_count} ${t("col_website").toLowerCase()}(s)</span>` : ""}
            </div>
          `;
        }).join("")
      : `<div class="muted">${escape(t("no_tools_captured"))}</div>`;

    // Sources — one panel per quoted website. Pending profiles get a hint.
    const sourcesBlock = (a.sources || []).map(s => {
      const auth = s.authority_score
        ? `<div class="auth-bar mini" title="${escape(t("trust_score"))}: ${s.authority_score}/100"><div class="auth-fill" style="width:${s.authority_score}%"></div></div><span class="auth-num">${s.authority_score}</span>`
        : `<span class="muted">—</span>`;
      const kindBadge = s.page_kind ? `<span class="badge kind-${escape(s.page_kind)}">${escape(humanizeKind(s.page_kind))}</span>` : "";
      const langBadge = s.html_lang ? `<span class="badge lang">${escape(s.html_lang)}</span>` : "";
      // Schema badges in plain English. We hide the "schema.org/" prefix
      // entirely — marketing folks read "Product info" much faster than
      // "schema.org/Product".
      const schemaBadges = [
        s.has_schema_product && `<span class="badge schema" title="schema.org/Product">Product info</span>`,
        s.has_schema_review  && `<span class="badge schema" title="schema.org/Review">Reviews</span>`,
        s.has_schema_article && `<span class="badge schema" title="schema.org/Article">Article</span>`,
      ].filter(Boolean).join("");
      const youPill = s.user_present
        ? `<span class="you-badge present">✓ ${escape(a.user_brand)} ${escape(t("you_on_page"))}</span>`
        : `<span class="you-badge absent">✗ ${escape(a.user_brand)} ${escape(t("you_not_on_page"))}</span>`;
      const brandHits = (s.brand_hits || []).map(h => `
        <div class="brand-hit ${h.is_user ? "you" : ""}">
          <div class="bh-head">
            <span class="bh-brand">${escape(h.brand)}${h.is_user ? ' <span class="you-tag">YOU</span>' : ""}</span>
            <span class="bh-count">${h.count}×</span>
            <span class="bh-sent">
              ${h.positive ? `<span class="sent pos" title="positive mentions">+${h.positive}</span>` : ""}
              ${h.neutral  ? `<span class="sent neu" title="neutral mentions">·${h.neutral}</span>`   : ""}
              ${h.negative ? `<span class="sent neg" title="negative mentions">−${h.negative}</span>` : ""}
            </span>
          </div>
          ${h.snippets && h.snippets[0] ? `<div class="bh-snippet">${escape(clip(h.snippets[0], 180))}</div>` : ""}
        </div>
      `).join("");
      const pending = s.pending
        ? `<div class="help"><b>${escape(t("profile_pending"))}</b></div>`
        : "";
      const errBox = s.err
        ? `<div class="help warn"><b>${escape(t("crawl_error"))}:</b> ${escape(s.err)}${s.status_code ? ` (HTTP ${s.status_code})` : ""}</div>`
        : "";
      return `
        <div class="anatomy-source">
          <div class="anatomy-source-head">
            <a href="${escape(s.url)}" target="_blank" rel="noopener" class="anatomy-source-url">${escape(s.url)}</a>
            <div class="anatomy-source-badges">${kindBadge}${langBadge}${schemaBadges}</div>
          </div>
          ${s.title ? `<div class="anatomy-source-title">${escape(s.title)}</div>` : ""}
          ${s.description ? `<div class="anatomy-source-desc">${escape(clip(s.description, 180))}</div>` : ""}
          <div class="anatomy-source-meta">
            <span>${escape(t("trust_score"))} ${auth}</span>
            ${s.word_count ? `<span class="muted">${s.word_count} ${escape(t("words"))}</span>` : ""}
            ${youPill}
          </div>
          ${pending}${errBox}
          ${brandHits ? `<div class="brand-hits">${brandHits}</div>` : ""}
        </div>
      `;
    }).join("");

    // Findings ("diagnoses" on the wire — UI calls it "What to do").
    // Severity labels are translated so the user sees "fix this" / "worth
    // a look" / "FYI" instead of the dev-y critical/warn/info.
    const sevLabel = (sev) => sev === "critical" ? t("sev_critical")
                       : sev === "warn"     ? t("sev_warn")
                       : t("sev_info");
    const diagBlock = (a.diagnoses && a.diagnoses.length > 0)
      ? a.diagnoses.map(d => `
          <div class="diagnosis sev-${escape(d.severity)}">
            <div class="diag-head">
              <span class="diag-icon">${d.severity === "critical" ? "⚠" : d.severity === "warn" ? "⚡" : "ℹ"}</span>
              <span class="diag-title">${escape(d.title)}</span>
              <span class="badge sev-${escape(d.severity)}">${escape(sevLabel(d.severity))}</span>
            </div>
            <div class="diag-detail">${escape(d.detail)}</div>
            ${d.action ? `<div class="diag-action"><b>What to do:</b> ${escape(d.action)}</div>` : ""}
          </div>
        `).join("")
      : `<div class="muted">${escape(t("no_findings"))}</div>`;

    const promptBox = `
      <div class="anatomy-section">
        <div class="anatomy-section-head">1. ${escape(t("anatomy_step_prompt"))}</div>
        <div class="anatomy-prompt-region">${escape(a.region_label)}${a.region !== "global" ? ` · region context added` : ""}</div>
        <pre class="anatomy-prompt">${escape(a.prompt_sent)}</pre>
      </div>
    `;
    const toolBox = `
      <div class="anatomy-section">
        <div class="anatomy-section-head">2. ${escape(t("anatomy_step_tools"))}</div>
        ${toolCallsBlock}
      </div>
    `;
    const srcBox = `
      <div class="anatomy-section">
        <div class="anatomy-section-head">3. ${escape(t("anatomy_step_sources"))} (${(a.sources || []).length})</div>
        ${sourcesBlock || `<div class="muted">${escape(t("no_sources_cited"))}</div>`}
      </div>
    `;
    const answerBox = `
      <div class="anatomy-section">
        <div class="anatomy-section-head">4. ${escape(t("anatomy_step_answer"))}</div>
        <pre class="anatomy-answer">${escape(a.response_text || "")}</pre>
      </div>
    `;
    const diagSection = `
      <div class="anatomy-section">
        <div class="anatomy-section-head">5. ${escape(t("anatomy_step_diagnosis"))}</div>
        ${diagBlock}
      </div>
    `;
    $("#modalBody").innerHTML = promptBox + toolBox + srcBox + answerBox + diagSection;
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
  // REGION_PRESETS mirrors regions.Presets() server-side. flag + native
  // are dashboard-only metadata used by the country-picker UI (like the
  // language switcher on most i18n websites). Server only cares about
  // {code, label, prefix}; flag/native are dropped on save.
  const REGION_PRESETS = [
    { code: "global", label: "Global",         flag: "🌐", native: "every locale", prefix: "" },
    { code: "us",     label: "United States",  flag: "🇺🇸", native: "English",       prefix: "Context: I am asking from the United States." },
    { code: "uk",     label: "United Kingdom", flag: "🇬🇧", native: "English",       prefix: "Context: I am asking from the United Kingdom." },
    { code: "in",     label: "India",          flag: "🇮🇳", native: "हिन्दी / English", prefix: "Context: I am asking from India." },
    { code: "jp",     label: "Japan",          flag: "🇯🇵", native: "日本語",         prefix: "前提: 私は日本から尋ねています。" },
    { code: "de",     label: "Germany",        flag: "🇩🇪", native: "Deutsch",       prefix: "Hinweis: Ich frage aus Deutschland." },
    { code: "fr",     label: "France",         flag: "🇫🇷", native: "Français",      prefix: "Contexte: je pose la question depuis la France." },
    { code: "br",     label: "Brazil",         flag: "🇧🇷", native: "Português",     prefix: "Contexto: estou perguntando do Brasil." },
    { code: "mx",     label: "Mexico",         flag: "🇲🇽", native: "Español",       prefix: "Contexto: pregunto desde México." },
    { code: "id",     label: "Indonesia",      flag: "🇮🇩", native: "Bahasa",        prefix: "Konteks: saya bertanya dari Indonesia." },
    { code: "kr",     label: "South Korea",    flag: "🇰🇷", native: "한국어",         prefix: "맥락: 저는 한국에서 묻고 있습니다." },
  ];

  // Look up display metadata for a region code. Falls back to a generic
  // entry so unknown codes render gracefully instead of breaking the UI.
  function regionDisplay(code) {
    if (!code) {
      return { code: "", label: "All regions", flag: "🌐", native: "every locale" };
    }
    const match = REGION_PRESETS.find(r => r.code === code);
    if (match) return match;
    return { code, label: code, flag: "🏳", native: "" };
  }

  function showProjectForm(existing) {
    const p = existing || {
      name: "", brand: { name: "", aliases: [] },
      competitors: [], prompts: [],
      providers: [{ name: "openai-default", kind: "openai", model: "gpt-4.1-mini" }],
      // New projects default to Global + US + Japan so the user immediately
      // sees the multi-region workflow. They can untoggle ones they don't
      // care about.
      regions: [
        { code: "global", label: "Global",        prefix: "" },
        { code: "us",     label: "United States", prefix: "Context: I am asking from the United States." },
        { code: "jp",     label: "Japan",         prefix: "前提: 私は日本から尋ねています。" },
      ],
      samples_per_prompt: 5, concurrency_per_provider: 3, max_tokens: 512, notes: ""
    };
    const isEdit = !!existing;
    const activeRegionCodes = new Set((p.regions || []).map(r => r.code));

    const regionChips = REGION_PRESETS.map(r => `
      <label class="region-chip ${activeRegionCodes.has(r.code) ? "active" : ""}">
        <input type="checkbox" data-region-code="${escape(r.code)}" ${activeRegionCodes.has(r.code) ? "checked" : ""}>
        <span>${escape(r.label)}</span>
      </label>
    `).join("");

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
        <label>Regions to track</label>
        <div class="region-chip-grid" id="pf_regions">${regionChips}</div>
        <div class="hint">
          Each selected region multiplies the bench cost (more locales = more LLM calls). Pick the markets
          you actually care about. <b>Global</b> uses no region prefix; the others nudge the LLM with a
          locale hint so it answers in the right language with region-relevant brands.
        </div>
      </div>
      <div class="form-row">
        <label>Competitors (one per line — format: <code>Name | aliases | regions</code>)</label>
        <textarea id="pf_competitors" placeholder="Kao Curél | 花王, Curél | jp&#10;Mamaearth | | in&#10;Dove | | "
        >${escape((p.competitors||[]).map(c => {
          const aliases = (c.aliases||[]).join(", ");
          const regions = (c.regions||[]).join(", ");
          return `${c.name} | ${aliases} | ${regions}`;
        }).join("\n"))}</textarea>
        <div class="hint">
          Third column is a comma-separated list of region codes the competitor competes in.
          Leave it empty to mean "applies to every region" (a true global brand like Dove).
          A JP-only brand like Tsubaki should have <code>jp</code> so it doesn't appear in your India report.
        </div>
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
        // Region chip toggle: click to add the .active class so the
        // visual matches the underlying checkbox state.
        document.querySelectorAll("#pf_regions .region-chip").forEach(el => {
          const cb = el.querySelector("input[type=checkbox]");
          el.addEventListener("click", (ev) => {
            if (ev.target !== cb) cb.checked = !cb.checked;
            el.classList.toggle("active", cb.checked);
          });
        });
        if (isEdit) {
          $("#pf_delete").addEventListener("click", () => {
            if (!confirm(`Delete project "${p.name}" and all its runs? This cannot be undone.`)) return;
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
    // Format: Name | alias1, alias2 | region1, region2
    // The third column (regions) is optional. Empty means "applies everywhere".
    // Accepts either short codes ("jp") or full labels ("Japan", "japan") —
    // normalized via normalizeRegionInput so users don't have to remember
    // the two-letter codes.
    return s.split("\n").map(l => l.trim()).filter(Boolean).map(line => {
      const parts = line.split("|").map(x => (x || "").trim());
      const [name, aliasesStr, regionsStr] = parts;
      const regions = regionsStr
        ? regionsStr.split(",").map(x => normalizeRegionInput(x)).filter(Boolean)
        : [];
      return {
        name,
        aliases: aliasesStr ? aliasesStr.split(",").map(x => x.trim()).filter(Boolean) : [],
        regions,
      };
    });
  }

  // normalizeRegionInput accepts "jp" / "JP" / "Japan" / "japan" / " Japan "
  // and returns the canonical short code ("jp"). Returns "" if the input
  // doesn't match any known region.
  function normalizeRegionInput(s) {
    const raw = (s || "").trim().toLowerCase();
    if (raw === "") return "";
    for (const r of REGION_PRESETS) {
      if (r.code === raw) return r.code;
      if (r.label.toLowerCase() === raw) return r.code;
    }
    // Unknown region — pass through as-is so the server can still accept
    // custom region codes if a project wires its own up.
    return raw;
  }

  function parseRegionsFromForm() {
    const out = [];
    document.querySelectorAll("#pf_regions input[type=checkbox]:checked").forEach(cb => {
      const code = cb.dataset.regionCode;
      const preset = REGION_PRESETS.find(r => r.code === code);
      if (preset) out.push({ code: preset.code, label: preset.label, prefix: preset.prefix });
    });
    return out;
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
      regions: parseRegionsFromForm(),
      samples_per_prompt: Number($("#pf_samples").value) || 5,
      concurrency_per_provider: Number($("#pf_parallel").value) || 3,
      max_tokens: Number($("#pf_maxtok").value) || 512,
      notes: $("#pf_notes").value.trim(),
    };
    if (!body.name || !body.brand.name) {
      toast("Project name and brand name are required");
      return;
    }
    if (body.regions.length === 0) {
      toast("Pick at least one region — usually 'Global' is the minimum");
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

      <div style="display:flex;gap:8px;margin-top:8px;align-items:center">
        <button class="btn-primary" id="settings_edit" type="button">Edit project</button>
        <button class="btn-secondary" id="settings_explain" type="button">Run explain</button>
        <button class="btn-secondary" id="settings_advise" type="button">Run advise</button>
        <div style="flex:1"></div>
        <button class="btn-danger" id="settings_delete" type="button">Delete project</button>
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
    $("#settings_delete").addEventListener("click", () => {
      if (!confirm(
        `Delete project "${p.name}" and ALL its runs / samples / mentions / actions?\n\n` +
        `This cannot be undone.`)) return;
      api.deleteProject(p.id).then(() => {
        toast(`Deleted ${p.name}`);
        state.selectedProject = null;
        state.selectedRun = null;
        refreshAll();
      }).catch(e => toast("Delete failed: " + e.message));
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

  // Initial load. Language must initialise before any t() call so the very
  // first paint of the sidebar uses the user's preferred language.
  state.language = detectLanguage();
  setupRegionPicker();
  setupLanguagePicker();
  updateLanguagePicker();
  applyStaticTranslations();
  (async () => {
    state.projects = await api.projects();
    state.selectedProject = state.projects[0] || null;
    // Auto-detect the user's market region from their browser (timezone +
    // locale). Honours an explicit localStorage choice. Only applies a
    // detected region if the active project actually has it configured.
    applyRegionAutoDetect();
    renderProjectPicker();
    await refreshRunsForProject();
    updateRunNowEnabled();
    renderMain();
    startSSE();
  })();
})();
