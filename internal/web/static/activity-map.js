// Interactive route map for the activity detail overlay: any element with
// data-route='[[lat,lng],...]' becomes a Leaflet map (vendored
// static/leaflet.js) on OpenStreetMap tiles, with the route drawn as a line
// plus start/end dots. Watches the DOM rather than running inline, since
// the overlay's content arrives as a Datastar SSE patch. Only map tiles are
// fetched from OSM — the route itself never leaves the browser.
const initRouteMaps = () => {
  for (const el of document.querySelectorAll("[data-route]:not(.leaflet-container)")) {
    const route = JSON.parse(el.dataset.route);
    const map = L.map(el);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    }).addTo(map);
    const line = L.polyline(route, { color: "#3b82f6", weight: 4 }).addTo(map);
    L.circleMarker(route[0], { radius: 6, color: "#16a34a", fillOpacity: 1 }).addTo(map).bindTooltip("Start");
    L.circleMarker(route[route.length - 1], { radius: 6, color: "#dc2626", fillOpacity: 1 }).addTo(map).bindTooltip("End");
    map.fitBounds(line.getBounds(), { padding: [20, 20] });
    // The overlay may still be laying out when the patch lands; re-measure
    // once it has, or Leaflet renders tiles for a zero-size box.
    setTimeout(() => map.invalidateSize(), 100);
  }
};
new MutationObserver(initRouteMaps).observe(document.documentElement, { childList: true, subtree: true });
