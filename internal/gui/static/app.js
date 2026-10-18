(() => {
  const panel = document.getElementById("panel");
  const pad = (n) => String(n).padStart(2, "0");
  const fmt = (s) => {
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = s % 60;
    return h ? `${h}:${pad(m)}:${pad(r)}` : `${pad(m)}:${pad(r)}`;
  };
  const state = () => panel.querySelector("[data-state]")?.dataset.state;

  // Swap the panel only when the session state changed, so typing is not lost.
  async function sync() {
    try {
      const res = await fetch("/state", { cache: "no-store" });
      const next = await res.text();
      if (next !== state()) htmx.ajax("GET", "/panel", { target: "#panel" });
    } catch {}
  }

  function tick() {
    const el = panel.querySelector("[data-ends-at]");
    if (!el) return;
    const now = Date.now() / 1000;
    const end = +el.dataset.endsAt, start = +el.dataset.startedAt;
    const left = Math.max(0, Math.ceil(end - now));
    el.textContent = fmt(left);
    const bar = panel.querySelector(".bar > div");
    if (bar) bar.style.width = `${Math.min(100, ((now - start) / (end - start)) * 100)}%`;
    document.title = left ? `${fmt(left)} · Focus` : "Focus";
    if (left === 0) sync();
  }
  setInterval(tick, 1000);
  tick();

  document.body.addEventListener("htmx:afterSettle", () => {
    tick();
    const card = panel.querySelector("[data-close-after]");
    if (card && window.focusClose) setTimeout(() => window.focusClose(), +card.dataset.closeAfter);
    panel.querySelector("[autofocus]")?.focus();
  });

  const events = new EventSource("/events");
  events.addEventListener("change", sync);
  events.addEventListener("open", sync);
})();
