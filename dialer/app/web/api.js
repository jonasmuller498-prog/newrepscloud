(() => {
  const token = role => sessionStorage.getItem(`${role}Token`) || "";

  async function request(path, options = {}, role = "operator") {
    const headers = new Headers(options.headers || {});
    headers.set("Authorization", `Bearer ${token(role)}`);
    if (options.body && typeof options.body === "string") {
      headers.set("Content-Type", "application/json");
    }
    const response = await fetch(path, {...options, headers});
    const type = response.headers.get("content-type") || "";
    const body = type.includes("json") ? await response.json() : await response.text();
    if (!response.ok) {
      const error = new Error(body.message || body || `HTTP ${response.status}`);
      error.problem = body;
      throw error;
    }
    return body;
  }

  function saveTokens() {
    sessionStorage.setItem("operatorToken", document.querySelector("#operator-token").value);
    sessionStorage.setItem("approverToken", document.querySelector("#approver-token").value);
  }

  function restoreTokens() {
    document.querySelector("#operator-token").value = token("operator");
    document.querySelector("#approver-token").value = token("approver");
  }

  window.DialerAPI = {request, saveTokens, restoreTokens};
})();
