// Keeps x-axis labels from overlapping at any chart width: the server sends
// every label (it can't know how wide a resizable tile will be), and this
// hides each label that would touch the last one kept, left to right — the
// first label always stays. Measured on the real rendered text, so it holds
// for every chart type, label length and font. Re-runs when a chart is
// resized (ResizeObserver) or re-rendered by a Datastar patch.
const thinLabels = (wrap) => {
  let lastRight = -Infinity;
  for (const label of wrap.querySelectorAll(".chart-x-label")) {
    label.style.visibility = "";
    const r = label.getBoundingClientRect();
    if (r.left < lastRight + 6) {
      label.style.visibility = "hidden";
    } else {
      lastRight = r.right;
    }
  }
};
const resized = new ResizeObserver((entries) => entries.forEach((e) => thinLabels(e.target)));
let queued = false;
const thinAll = () => {
  queued = false;
  for (const wrap of document.querySelectorAll(".chart-wrap")) {
    resized.observe(wrap);
    thinLabels(wrap);
  }
};
new MutationObserver(() => {
  if (!queued) {
    queued = true;
    requestAnimationFrame(thinAll);
  }
}).observe(document.documentElement, { childList: true, subtree: true });
thinAll();
