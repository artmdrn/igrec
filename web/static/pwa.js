"use strict";

(function () {
  if (!("serviceWorker" in navigator)) return;

  function urlBase64ToUint8Array(value) {
    var padding = "=".repeat((4 - value.length % 4) % 4);
    var base64 = (value + padding).replace(/-/g, "+").replace(/_/g, "/");
    var raw = window.atob(base64);
    var output = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i += 1) output[i] = raw.charCodeAt(i);
    return output;
  }

  function encodeForm(data) {
    var form = new URLSearchParams();
    Object.keys(data).forEach(function (key) {
      form.set(key, data[key]);
    });
    return form;
  }

  function setPushCount(countNode, count) {
    if (!countNode) return;
    countNode.textContent = count + (count === 1 ? " device ready" : " devices ready");
  }

  function setupPushControls(registration) {
    var button = document.querySelector("[data-push-toggle]");
    if (!button) return;

    var status = document.querySelector("[data-push-status]");
    var countNode = document.querySelector("[data-push-count]");
    var vapidKey = button.getAttribute("data-push-vapid");
    var csrfToken = button.getAttribute("data-push-csrf");

    if (!("PushManager" in window) || !("Notification" in window)) {
      button.disabled = true;
      button.textContent = "browser notifications unavailable";
      if (status) status.textContent = "This browser does not support push subscriptions.";
      return;
    }

    function setBusy(isBusy) {
      button.disabled = isBusy;
    }

    function setMode(hasSubscription) {
      button.textContent = hasSubscription ? "disable browser notifications" : "enable browser notifications";
      if (!hasSubscription && status && !status.textContent) {
        status.textContent = "Store this browser so push delivery can be wired in next.";
      }
    }

    function post(path, data) {
      return fetch(path, {
        method: "POST",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8"
        },
        body: encodeForm(data)
      }).then(function (response) {
        if (!response.ok) {
          return response.text().then(function (text) {
            throw new Error(text || "request failed");
          });
        }
        return response.json();
      });
    }

    registration.pushManager.getSubscription().then(function (subscription) {
      setMode(Boolean(subscription));
    }).catch(function () {
      if (status) status.textContent = "Could not inspect browser notification state.";
    });

    button.addEventListener("click", function () {
      setBusy(true);
      registration.pushManager.getSubscription().then(function (subscription) {
        if (subscription) {
          var endpoint = subscription.endpoint;
          return subscription.unsubscribe().catch(function () {
          }).then(function () {
            return post("/push/unsubscribe", {
              csrf_token: csrfToken,
              endpoint: endpoint
            });
          }).then(function (payload) {
            setPushCount(countNode, payload.subscription_count || 0);
            setMode(false);
            if (status) status.textContent = "This browser will stop receiving igrec push notifications.";
          });
        }

        return Notification.requestPermission().then(function (permission) {
          if (permission !== "granted") {
            throw new Error("notification permission denied");
          }
          return registration.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(vapidKey)
          });
        }).then(function (nextSubscription) {
          var json = nextSubscription.toJSON();
          return post("/push/subscribe", {
            csrf_token: csrfToken,
            endpoint: json.endpoint || "",
            p256dh: json.keys && json.keys.p256dh ? json.keys.p256dh : "",
            auth: json.keys && json.keys.auth ? json.keys.auth : ""
          });
        }).then(function (payload) {
          setPushCount(countNode, payload.subscription_count || 0);
          setMode(true);
          if (status) status.textContent = "This browser is ready for igrec push notifications.";
        });
      }).catch(function (error) {
        if (status) status.textContent = error && error.message ? error.message : "Notification setup failed.";
      }).finally(function () {
        setBusy(false);
      });
    });
  }

  window.addEventListener("load", function () {
    navigator.serviceWorker.register("/service-worker.js").then(function (registration) {
      setupPushControls(registration);
    }).catch(function () {
    });
  });
})();
