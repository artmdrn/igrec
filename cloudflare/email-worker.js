export default {
  async email(message, env) {
    const raw = await new Response(message.raw).text();
    const body = firstTextBody(raw);

    await fetch(env.IGREC_INBOUND_URL, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "x-igrec-secret": env.APP_SECRET
      },
      body: JSON.stringify({
        from: message.from,
        to: message.to,
        text: body
      })
    });
  }
};

function firstTextBody(raw) {
  const marker = "\r\n\r\n";
  const idx = raw.indexOf(marker);
  const body = idx >= 0 ? raw.slice(idx + marker.length) : raw;
  return body
    .split("\n")
    .filter((line) => !line.trim().startsWith(">"))
    .join("\n")
    .trim();
}
