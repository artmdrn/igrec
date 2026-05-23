function b64urlToBuffer(value) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - value.length % 4) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

function bufferToB64url(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function creationOptions(options) {
  options.publicKey.challenge = b64urlToBuffer(options.publicKey.challenge);
  options.publicKey.user.id = b64urlToBuffer(options.publicKey.user.id);
  for (const credential of options.publicKey.excludeCredentials || []) {
    credential.id = b64urlToBuffer(credential.id);
  }
  return options.publicKey;
}

function assertionOptions(options) {
  options.publicKey.challenge = b64urlToBuffer(options.publicKey.challenge);
  for (const credential of options.publicKey.allowCredentials || []) {
    credential.id = b64urlToBuffer(credential.id);
  }
  return options.publicKey;
}

function creationResponse(credential) {
  return {
    id: credential.id,
    rawId: bufferToB64url(credential.rawId),
    type: credential.type,
    response: {
      attestationObject: bufferToB64url(credential.response.attestationObject),
      clientDataJSON: bufferToB64url(credential.response.clientDataJSON)
    },
    clientExtensionResults: credential.getClientExtensionResults()
  };
}

function assertionResponse(credential) {
  return {
    id: credential.id,
    rawId: bufferToB64url(credential.rawId),
    type: credential.type,
    response: {
      authenticatorData: bufferToB64url(credential.response.authenticatorData),
      clientDataJSON: bufferToB64url(credential.response.clientDataJSON),
      signature: bufferToB64url(credential.response.signature),
      userHandle: credential.response.userHandle ? bufferToB64url(credential.response.userHandle) : null
    },
    clientExtensionResults: credential.getClientExtensionResults()
  };
}

async function postJSON(url, body) {
  const response = await fetch(url, {
    method: "POST",
    credentials: "same-origin",
    headers: {"content-type": "application/json"},
    body: body ? JSON.stringify(body) : "{}"
  });
  if (!response.ok) throw new Error(await response.text());
  return response.json();
}

async function addPasskey(button) {
  if (!window.PublicKeyCredential) throw new Error("passkeys are not available");
  button.disabled = true;
  try {
    const options = await postJSON("/auth/passkeys/register/options");
    const credential = await navigator.credentials.create({publicKey: creationOptions(options)});
    await postJSON("/auth/passkeys/register", creationResponse(credential));
    location.reload();
  } finally {
    button.disabled = false;
  }
}

async function loginWithPasskey(button) {
  if (!window.PublicKeyCredential) throw new Error("passkeys are not available");
  button.disabled = true;
  try {
    const next = new URLSearchParams(location.search).get("next") || "/write";
    const options = await postJSON("/auth/passkeys/login/options");
    const credential = await navigator.credentials.get({publicKey: assertionOptions(options)});
    const result = await postJSON("/auth/passkeys/login?next=" + encodeURIComponent(next), assertionResponse(credential));
    location.href = result.next || next;
  } finally {
    button.disabled = false;
  }
}

document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-passkey]");
  if (!button) return;
  event.preventDefault();
  try {
    if (button.dataset.passkey === "register") await addPasskey(button);
    if (button.dataset.passkey === "login") await loginWithPasskey(button);
  } catch (error) {
    const status = document.querySelector("[data-passkey-status]");
    if (status) status.textContent = String(error.message || error);
  }
});
