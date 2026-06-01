export default {
  async email(message, env) {
    const raw = await new Response(message.raw).text();
    const body = firstTextBody(raw);
    const attachments = imageAttachments(raw).slice(0, 1);

    const response = await fetch(env.IGREC_INBOUND_URL, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-igrec-secret": env.APP_SECRET
      },
      body: JSON.stringify({
        from: message.from,
        to: message.to,
        text: body,
        attachments
      })
    });
    if (!response.ok) {
      throw new Error(`igrec inbound returned ${response.status}: ${await response.text()}`);
    }
  }
};

function firstTextBody(raw) {
  const normalized = raw.replace(/\r\n/g, "\n");
  const headers = headerMap(headerBlock(normalized));
  const contentType = headers.get("content-type") || "";
  const boundary = boundaryFrom(contentType);
  if (boundary) {
    for (const part of multipartParts(normalized, boundary)) {
      const partHeaders = headerMap(headerBlock(part));
      const partType = (partHeaders.get("content-type") || "text/plain").toLowerCase();
      if (partType.startsWith("text/plain")) {
        return cleanBody(decodeTransfer(partBody(part), partHeaders.get("content-transfer-encoding") || ""));
      }
    }
  }
  return cleanBody(partBody(normalized));
}

function imageAttachments(raw) {
  const found = [];
  collectImageParts(raw.replace(/\r\n/g, "\n"), found, 0);
  return found;
}

function collectImageParts(raw, found, depth) {
  if (depth > 4 || found.length >= 1) return;
  const headers = headerMap(headerBlock(raw));
  const contentType = headers.get("content-type") || "";
  const boundary = boundaryFrom(contentType);
  if (boundary) {
    for (const part of multipartParts(raw, boundary)) {
      collectImageParts(part, found, depth + 1);
      if (found.length >= 1) return;
    }
    return;
  }

  const partType = contentType.toLowerCase();
  if (!partType.startsWith("image/jpeg") && !partType.startsWith("image/png")) return;
  const transfer = (headers.get("content-transfer-encoding") || "").toLowerCase();
  if (transfer !== "base64") return;
  found.push({
    filename: filenameFrom(headers.get("content-disposition") || "") || filenameFrom(contentType),
    content_type: partType.split(";")[0],
    data: partBody(raw).replace(/\s/g, "")
  });
}

function headerBlock(raw) {
  const idx = raw.indexOf("\n\n");
  return idx >= 0 ? raw.slice(0, idx) : "";
}

function partBody(raw) {
  const idx = raw.indexOf("\n\n");
  return idx >= 0 ? raw.slice(idx + 2) : raw;
}

function headerMap(block) {
  const map = new Map();
  let last = "";
  for (const line of block.split("\n")) {
    if (/^\s/.test(line) && last) {
      map.set(last, `${map.get(last)} ${line.trim()}`);
      continue;
    }
    const idx = line.indexOf(":");
    if (idx < 0) continue;
    last = line.slice(0, idx).toLowerCase();
    map.set(last, line.slice(idx + 1).trim());
  }
  return map;
}

function boundaryFrom(contentType) {
  const match = contentType.match(/boundary=(?:"([^"]+)"|([^;\s]+))/i);
  return match ? (match[1] || match[2]) : "";
}

function filenameFrom(value) {
  const match = value.match(/filename=(?:"([^"]+)"|([^;\s]+))/i) || value.match(/name=(?:"([^"]+)"|([^;\s]+))/i);
  return match ? (match[1] || match[2]) : "";
}

function multipartParts(raw, boundary) {
  return raw
    .split(`--${boundary}`)
    .slice(1)
    .filter((part) => !part.trim().startsWith("--"))
    .map((part) => part.replace(/^\n/, ""));
}

function decodeTransfer(body, encoding) {
  if (encoding.toLowerCase() === "base64") {
    try {
      return atob(body.replace(/\s/g, ""));
    } catch {
      return body;
    }
  }
  if (encoding.toLowerCase() === "quoted-printable") {
    return body
      .replace(/=\n/g, "")
      .replace(/=([0-9A-F]{2})/gi, (_, hex) => String.fromCharCode(parseInt(hex, 16)));
  }
  return body;
}

function cleanBody(body) {
  return body
    .split("\n")
    .filter((line) => !line.trim().startsWith(">"))
    .filter((line) => !/^On .+ wrote:$/i.test(line.trim()))
    .filter((line) => !/^--\s*$/.test(line.trim()))
    .join("\n")
    .trim();
}
