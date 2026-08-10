(() => {
  function client(role, inputSelector) {
    const key = `${role}Token`;
    const token = () => sessionStorage.getItem(key) || "";
    async function request(path, options = {}) {
      const headers = new Headers(options.headers || {});
      headers.set("Authorization", `Bearer ${token()}`);
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
    function saveToken() {
      sessionStorage.setItem(key, document.querySelector(inputSelector).value);
    }
    function restoreToken() {
      document.querySelector(inputSelector).value = token();
    }
    return {request, saveToken, restoreToken, hasToken: () => Boolean(token())};
  }

  window.DialerAPI = {client};
})();
