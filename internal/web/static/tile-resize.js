// Drag-to-resize for expanded dashboard tiles: the right edge sets width,
// the bottom edge height (a minimum — a tile never gets shorter than its
// own content, see style.css), the corner both. Width snaps to twelfths of the
// row (the dashboard's fixed 12-column grid) and height to 20px steps,
// both applied live while dragging; the final size is saved once on
// release (POST data-resize-url, see handleTileResize). Plain DOM rather
// than Datastar expressions: this is continuous pointer tracking with
// layout math, which would be unreadable inline in an attribute.
document.addEventListener("pointerdown", (e) => {
  const handle = e.target.closest(".tile-resize-handle");
  if (!handle) return;
  e.preventDefault();
  const tile = handle.closest(".tile");
  const mode = handle.dataset.resize;

  const cs = getComputedStyle(tile.parentElement);
  const cols = 12;
  const gap = parseFloat(cs.columnGap) || 0;
  const colW = parseFloat(cs.gridTemplateColumns);

  const startX = e.clientX, startY = e.clientY;
  const startW = tile.offsetWidth, startH = tile.offsetHeight;
  // Narrowest allowed tile: 2 twelfths (a single twelfth is too thin for
  // a tile header).
  const clamp = (span) => Math.min(Math.max(span, 2), cols);
  let width = clamp(Math.round((startW + gap) / (colW + gap)));
  let height = parseInt(tile.style.minHeight) || 0;

  handle.setPointerCapture(e.pointerId);
  document.body.classList.add("tile-resizing");

  const move = (ev) => {
    if (mode.includes("x")) {
      width = clamp(Math.round((startW + ev.clientX - startX + gap) / (colW + gap)));
      tile.className = tile.className.replace(/\s*tile-span-\d+/g, "") + " tile-span-" + width;
    }
    if (mode.includes("y")) {
      height = Math.min(Math.max(Math.round((startH + ev.clientY - startY) / 20) * 20, 120), 1200);
      tile.style.minHeight = height + "px";
    }
  };
  const up = () => {
    handle.removeEventListener("pointermove", move);
    handle.removeEventListener("pointerup", up);
    handle.removeEventListener("pointercancel", up);
    document.body.classList.remove("tile-resizing");
    const url = tile.dataset.resizeUrl + "&width=" + width + "&height=" + height;
    fetch(url, { method: "POST" }).catch(() => {});
  };
  handle.addEventListener("pointermove", move);
  handle.addEventListener("pointerup", up);
  handle.addEventListener("pointercancel", up);
});
