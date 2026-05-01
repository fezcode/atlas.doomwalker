// atlas · doomwalker — browser UI

const SVG_NS = "http://www.w3.org/2000/svg";

const palette = [
  "#7c3aed", "#a855f7", "#ec4899", "#f43f5e",
  "#ef4444", "#f97316", "#f59e0b", "#eab308",
  "#84cc16", "#10b981", "#14b8a6", "#06b6d4",
  "#0ea5e9", "#3b82f6", "#6366f1", "#8b5cf6",
];

// Stable color from name hash so the same folder is always the same hue.
function colorFor(name, isDir) {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0;
  const c = palette[Math.abs(h) % palette.length];
  return isDir ? c : mix(c, "#3f3f46", 0.55);
}
function mix(a, b, t) {
  const pa = parseInt(a.slice(1), 16), pb = parseInt(b.slice(1), 16);
  const ar = (pa>>16)&255, ag = (pa>>8)&255, ab = pa&255;
  const br = (pb>>16)&255, bg = (pb>>8)&255, bb = pb&255;
  const r = Math.round(ar*(1-t)+br*t), g = Math.round(ag*(1-t)+bg*t), bl = Math.round(ab*(1-t)+bb*t);
  return "#" + [r,g,bl].map(x => x.toString(16).padStart(2,"0")).join("");
}

function fmtSize(n) {
  if (n < 1024) return n + " B";
  const u = ["KiB","MiB","GiB","TiB","PiB"];
  let i = -1, s = n;
  while (s >= 1024 && i < u.length - 1) { s /= 1024; i++; }
  return s.toFixed(s >= 100 ? 0 : s >= 10 ? 1 : 2) + " " + u[i];
}
function fmtCount(n) { return n.toLocaleString("en-US"); }

// --- Squarified treemap (Bruls/Huijsen/van Wijk) ---------------------------
function squarify(items, x, y, w, h) {
  const total = items.reduce((s, it) => s + it.size, 0);
  if (total <= 0 || w <= 0 || h <= 0) return [];
  const scale = (w * h) / total;
  const sorted = items.map(it => ({...it, area: it.size * scale}))
                      .sort((a,b) => b.area - a.area);
  const out = [];
  let rx = x, ry = y, rw = w, rh = h;
  let i = 0;
  while (i < sorted.length) {
    let j = i + 1;
    while (j < sorted.length) {
      const cur = sorted.slice(i, j);
      const nxt = sorted.slice(i, j+1);
      if (worst(nxt, Math.min(rw, rh)) > worst(cur, Math.min(rw, rh))) break;
      j++;
    }
    [rx, ry, rw, rh] = layoutRow(sorted.slice(i, j), rx, ry, rw, rh, out);
    i = j;
  }
  return out;
}
function worst(row, side) {
  if (!row.length || side <= 0) return Infinity;
  let sum = 0, mx = 0, mn = Infinity;
  for (const r of row) { sum += r.area; if (r.area > mx) mx = r.area; if (r.area < mn) mn = r.area; }
  if (sum === 0 || mn === 0) return Infinity;
  const s2 = side * side;
  return Math.max((s2 * mx) / (sum * sum), (sum * sum) / (s2 * mn));
}
function layoutRow(row, x, y, w, h, out) {
  const sum = row.reduce((s, r) => s + r.area, 0);
  if (sum === 0) return [x, y, w, h];
  if (w <= h) {
    const stripH = sum / w;
    let cx = x;
    for (const r of row) {
      const rw = r.area / stripH;
      out.push({...r, x: cx, y, w: rw, h: stripH});
      cx += rw;
    }
    return [x, y + stripH, w, h - stripH];
  }
  const stripW = sum / h;
  let cy = y;
  for (const r of row) {
    const rh = r.area / stripW;
    out.push({...r, x, y: cy, w: stripW, h: rh});
    cy += rh;
  }
  return [x + stripW, y, w - stripW, h];
}

// --- App state -------------------------------------------------------------
const state = {
  current: null,    // server nodeDTO
  hovered: null,
  rects: [],        // current laid-out rects
};

const $ = id => document.getElementById(id);

async function fetchRoot() {
  const r = await fetch("/api/root");
  return r.json();
}
async function fetchNode(id) {
  const r = await fetch("/api/node?id=" + id);
  return r.json();
}

async function navigate(idOrNull) {
  const node = idOrNull == null ? await fetchRoot() : await fetchNode(idOrNull);
  state.current = node;
  render();
}

function render() {
  const n = state.current;
  if (!n) return;

  $("hero-name").textContent = n.name || "/";
  $("hero-size").textContent = fmtSize(n.size);
  const more = n.totalCount > n.children.length ? `, showing top ${n.children.length}` : "";
  $("hero-count").textContent = fmtCount(n.totalCount) + " items" + more;

  renderCrumbs(n.path);
  renderTreemap();
  renderRanking();
}

function renderCrumbs(path) {
  const el = $("crumbs");
  el.innerHTML = "";
  path.forEach((seg, i) => {
    if (i > 0) {
      const sep = document.createElement("span");
      sep.className = "sep"; sep.textContent = "›";
      el.appendChild(sep);
    }
    const a = document.createElement("span");
    a.className = "seg" + (i === path.length - 1 ? " current" : "");
    a.textContent = seg.name;
    a.onclick = () => navigate(seg.id);
    el.appendChild(a);
  });
}

function renderTreemap() {
  const svg = $("treemap");
  svg.innerHTML = "";
  const r = svg.getBoundingClientRect();
  const W = Math.max(100, Math.floor(r.width));
  const H = Math.max(100, Math.floor(r.height));
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);

  const items = state.current.children.map(c => ({...c}));
  if (!items.length) {
    const t = document.createElementNS(SVG_NS, "text");
    t.setAttribute("x", W/2); t.setAttribute("y", H/2);
    t.setAttribute("text-anchor", "middle"); t.setAttribute("fill", "#52525b");
    t.setAttribute("font-family", "Instrument Serif"); t.setAttribute("font-size", "32"); t.setAttribute("font-style", "italic");
    t.textContent = "empty";
    svg.appendChild(t);
    state.rects = [];
    return;
  }
  const rects = squarify(items, 0, 0, W, H);
  state.rects = rects;

  const largestId = rects.reduce((b, r) => (!b || r.size > b.size ? r : b), null)?.id;

  for (const rec of rects) {
    const g = document.createElementNS(SVG_NS, "g");
    const rect = document.createElementNS(SVG_NS, "rect");
    rect.setAttribute("x", rec.x);
    rect.setAttribute("y", rec.y);
    rect.setAttribute("width", Math.max(0, rec.w - 1));
    rect.setAttribute("height", Math.max(0, rec.h - 1));
    rect.setAttribute("fill", colorFor(rec.name, rec.isDir));
    rect.setAttribute("class", "tile " + (rec.isDir ? "dir" : "file") +
                              (rec.id === largestId ? " largest" : ""));
    rect.dataset.id = rec.id;
    rect.dataset.name = rec.name;
    rect.dataset.size = rec.size;
    rect.dataset.isDir = rec.isDir;
    g.appendChild(rect);

    if (rec.w > 60 && rec.h > 22) {
      const label = document.createElementNS(SVG_NS, "text");
      label.setAttribute("class", "tile-label");
      label.setAttribute("x", rec.x + 8);
      label.setAttribute("y", rec.y + 18);
      label.setAttribute("font-size", Math.min(14, rec.h / 4 + 8));
      label.textContent = truncate(rec.name, Math.floor(rec.w / 7));
      g.appendChild(label);
    }
    if (rec.w > 70 && rec.h > 38) {
      const sz = document.createElementNS(SVG_NS, "text");
      sz.setAttribute("class", "tile-size");
      sz.setAttribute("x", rec.x + 8);
      sz.setAttribute("y", rec.y + rec.h - 8);
      sz.setAttribute("font-size", "11");
      sz.textContent = fmtSize(rec.size);
      g.appendChild(sz);
    }
    svg.appendChild(g);
  }

  svg.onclick = (e) => {
    const t = e.target.closest("rect.tile");
    if (!t) return;
    if (t.dataset.isDir === "true") {
      navigate(parseInt(t.dataset.id, 10));
    }
  };
  svg.onmousemove = (e) => {
    const t = e.target.closest("rect.tile");
    if (!t) { hideTip(); return; }
    showTip(e, t);
  };
  svg.onmouseleave = hideTip;
}

function truncate(s, n) {
  if (s.length <= n) return s;
  if (n < 4) return s.slice(0, n);
  return s.slice(0, n - 1) + "…";
}

function showTip(e, rect) {
  const tip = $("tooltip");
  tip.hidden = false;
  tip.innerHTML = `
    <div class="name">${escapeHtml(rect.dataset.name)}</div>
    <div class="meta">${fmtSize(parseInt(rect.dataset.size, 10))} · ${rect.dataset.isDir === "true" ? "directory" : "file"}</div>
  `;
  const wrap = document.querySelector(".canvas-wrap").getBoundingClientRect();
  const x = e.clientX - wrap.left + 14;
  const y = e.clientY - wrap.top + 14;
  tip.style.left = Math.min(x, wrap.width - tip.offsetWidth - 8) + "px";
  tip.style.top  = Math.min(y, wrap.height - tip.offsetHeight - 8) + "px";
}
function hideTip() { $("tooltip").hidden = true; }

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
}

function renderRanking() {
  const ol = $("ranking");
  ol.innerHTML = "";
  const total = state.current.size || 1;
  state.current.children.slice(0, 50).forEach((c, i) => {
    const li = document.createElement("li");
    li.className = "rank-item " + (c.isDir ? "dir" : "file");
    li.innerHTML = `
      <span class="rank-num">${String(i+1).padStart(2,"0")}</span>
      <span class="rank-name"><span class="icon">${c.isDir ? "▸" : "·"}</span>${escapeHtml(c.name)}</span>
      <span class="rank-size">${fmtSize(c.size)}</span>
      <div class="rank-bar" style="--p:${(c.size/total*100).toFixed(2)}%"></div>
    `;
    li.onclick = () => { if (c.isDir) navigate(c.id); };
    ol.appendChild(li);
  });
}

// --- Keys & resize ---------------------------------------------------------
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape" || e.key === "Backspace") {
    const path = state.current?.path;
    if (path && path.length > 1) {
      navigate(path[path.length - 2].id);
    }
    e.preventDefault();
  }
  if (e.key >= "1" && e.key <= "9") {
    const i = parseInt(e.key, 10) - 1;
    const c = state.current?.children[i];
    if (c?.isDir) navigate(c.id);
  }
});
$("up").onclick = () => {
  const path = state.current?.path;
  if (path && path.length > 1) navigate(path[path.length - 2].id);
};

let resizeTimer = null;
window.addEventListener("resize", () => {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(renderTreemap, 80);
});

navigate(null);
