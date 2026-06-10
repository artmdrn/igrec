package app

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (a *App) manifest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, "application/manifest+json; charset=utf-8", map[string]any{
		"id":               "/write",
		"name":             "igrec",
		"short_name":       "igrec",
		"description":      "one word at a time.",
		"start_url":        "/write?source=pwa",
		"scope":            "/",
		"display":          "standalone",
		"display_override": []string{"standalone", "browser"},
		"background_color": "#f7f0df",
		"theme_color":      "#111111",
		"categories":       []string{"social", "productivity"},
		"shortcuts": []any{
			map[string]string{"name": "write", "short_name": "write", "url": "/write"},
			map[string]string{"name": "today", "short_name": "today", "url": "/today"},
			map[string]string{"name": "feed", "short_name": "feed", "url": "/"},
		},
		"icons": []any{
			map[string]string{"src": assetPath("icon-192.png"), "sizes": "192x192", "type": "image/png"},
			map[string]string{"src": assetPath("icon-512.png"), "sizes": "512x512", "type": "image/png"},
		},
	})
}

func (a *App) serviceWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	if r.Method == http.MethodHead {
		return
	}
	precache := []string{
		"/",
		"/today",
		"/write",
		"/manifest.webmanifest",
		assetPath("igrec.css"),
		assetPath("pwa.js"),
		assetPath("share.js"),
		assetPath("passkeys.js"),
		assetPath("compose.js"),
		assetPath("favicon-32.png"),
		assetPath("apple-touch-icon.png"),
		assetPath("icon-192.png"),
		assetPath("icon-512.png"),
	}
	precacheJSON, err := json.Marshal(precache)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	source := fmt.Sprintf(`"use strict";

const CACHE = "igrec-shell-%s";
const PRECACHE = %s;

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(PRECACHE)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key))),
    ).then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") return;
  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;

  if (event.request.mode === "navigate") {
    event.respondWith(
      fetch(event.request).then((response) => {
        const copy = response.clone();
        caches.open(CACHE).then((cache) => cache.put(event.request, copy));
        return response;
      }).catch(async () => {
        const cached = await caches.match(event.request);
        return cached || caches.match("/");
      }),
    );
    return;
  }

  event.respondWith(
    caches.match(event.request).then((cached) => cached || fetch(event.request).then((response) => {
      if (response.ok) {
        const copy = response.clone();
        caches.open(CACHE).then((cache) => cache.put(event.request, copy));
      }
      return response;
    })),
  );
});`, assetsVersion, precacheJSON)
	_, _ = w.Write([]byte(source))
}
