(() => {
  const {request, saveToken, restoreToken, hasToken} =
    window.DialerAPI.client("operator", "#operator-token");
  let campaigns = [];
  const $ = selector => document.querySelector(selector);
  const node = (tag, className, text) => {
    const item = document.createElement(tag);
    if (className) item.className = className;
    if (text !== undefined) item.textContent = text;
    return item;
  };
  const notice = text => { $("#notice").textContent = text; };
  const selectedCampaign = () => $("#campaign-select").value;

  function renderStatus(status) {
    const target = $("#status");
    target.replaceChildren();
    const values = {
      "Dialing": status.dialing_enabled ? "enabled" : "disabled",
      "CPS": status.cps,
      "Concurrency": status.max_concurrency,
      "ARI": status.ari_connected ? "connected" : "offline",
      "Leader": status.scheduler_leader ? "active" : "standby",
      "Campaigns": status.campaigns,
      "Queued": status.queued,
      "Active": status.active
    };
    Object.entries(values).forEach(([label, value]) => {
      const box = node("div", "metric");
      box.append(node("span", "", label), node("strong", "", String(value)));
      target.append(box);
    });
    renderBlocks(status.safety_blocks || []);
  }

  function renderBlocks(blocks) {
    const target = $("#blocks");
    target.replaceChildren();
    blocks.forEach(block => {
      target.append(node("div", "block", `${block.code}: ${block.message}${block.count ? ` (${block.count})` : ""}`));
    });
  }

  function renderCampaigns(items) {
    campaigns = items;
    const old = selectedCampaign();
    const select = $("#campaign-select");
    select.replaceChildren();
    items.forEach(campaign => {
      const option = node("option", "", `${campaign.name} — ${campaign.state}`);
      option.value = campaign.id;
      select.append(option);
    });
    if (items.some(item => item.id === old)) select.value = old;
    const list = $("#campaigns");
    list.replaceChildren();
    items.forEach(campaign => {
      const item = node("div", "item");
      item.append(node("span", "", campaign.name), node("small", "", `${campaign.state} · ${campaign.id}`));
      list.append(item);
    });
  }

  function renderSimple(selector, items, formatter) {
    const target = $(selector);
    target.replaceChildren();
    items.forEach(value => target.append(node("div", "item", formatter(value))));
  }

  async function refresh() {
    try {
      const [status, campaignItems, callerIDs, suppressions] = await Promise.all([
        request("/api/v1/status"), request("/api/v1/campaigns"),
        request("/api/v1/caller-ids"), request("/api/v1/suppressions")
      ]);
      renderStatus(status);
      renderCampaigns(campaignItems);
      renderSimple("#caller-ids", callerIDs,
        item => `${item.phone} · authorized ${new Date(item.authorized_at).toLocaleString()} · ${item.id}`);
      renderSimple("#suppressions", suppressions,
        item => `${item.phone} · ${item.reason} · ${new Date(item.created_at).toLocaleString()}`);
      await refreshCampaign();
      notice("Status refreshed.");
    } catch (error) {
      notice(error.message);
      if (error.problem?.blocks) renderBlocks(error.problem.blocks);
    }
  }

  async function refreshCampaign() {
    const id = selectedCampaign();
    if (!id) {
      $("#attempts").replaceChildren();
      return;
    }
    const [detail, attempts] = await Promise.all([
      request(`/api/v1/campaigns/${id}`),
      request(`/api/v1/attempts?campaign_id=${encodeURIComponent(id)}`)
    ]);
    if (detail.safety_blocks?.length) renderBlocks(detail.safety_blocks);
    renderSimple("#attempts", attempts,
      item => `${item.phone} · attempt ${item.attempt_no} · ${item.state}${item.outcome ? ` / ${item.outcome}` : ""}`);
  }

  async function submitJSON(event, path, transform = value => value) {
    event.preventDefault();
    const value = Object.fromEntries(new FormData(event.currentTarget));
    await request(path, {method: "POST", body: JSON.stringify(transform(value))});
    event.currentTarget.reset();
    await refresh();
  }

  $("#save-tokens").addEventListener("click", () => {
    saveToken();
    notice("Tokens saved in session storage for this tab.");
    refresh();
  });
  $("#refresh").addEventListener("click", refresh);
  $("#campaign-select").addEventListener("change", refreshCampaign);
  $("#campaign-form").addEventListener("submit", event => submitJSON(event, "/api/v1/campaigns", value => {
    if (!value.caller_id_id) delete value.caller_id_id;
    value.dnc_attested_at = new Date(value.dnc_attested_at).toISOString();
    return value;
  }).catch(error => notice(error.message)));
  $("#caller-form").addEventListener("submit", event => submitJSON(event, "/api/v1/caller-ids", value => {
    value.authorized_at = new Date(value.authorized_at).toISOString();
    return value;
  }).catch(error => notice(error.message)));
  $("#suppression-form").addEventListener("submit", event =>
    submitJSON(event, "/api/v1/suppressions").catch(error => notice(error.message)));

  $("#upload-csv").addEventListener("click", async () => {
    const file = $("#csv-file").files[0], id = selectedCampaign();
    if (!file || !id) return notice("Select a campaign and CSV file.");
    try {
      await request(`/api/v1/campaigns/${id}/recipients`, {
        method: "POST", body: file, headers: {"Idempotency-Key": crypto.randomUUID()}
      });
      notice("Recipient import queued."); await refresh();
    } catch (error) { notice(error.message); }
  });
  $("#upload-wav").addEventListener("click", async () => {
    const file = $("#wav-file").files[0], id = selectedCampaign();
    if (!file || !id) return notice("Select a campaign and WAV file.");
    try {
      await request(`/api/v1/campaigns/${id}/assets`, {method: "POST", body: file});
      notice("Validated audio attached."); await refresh();
    } catch (error) { notice(error.message); }
  });

  document.querySelectorAll("[data-action]").forEach(button => button.addEventListener("click", async () => {
    const action = button.dataset.action, id = selectedCampaign();
    if (!id) return notice("Select a campaign.");
    if ((action === "start" || action === "cancel") && !confirm(`Confirm ${action}?`)) return;
    let body;
    if (action === "schedule") {
      const value = $("#schedule-at").value;
      if (!value) return notice("Choose a schedule time.");
      body = JSON.stringify({scheduled_at: new Date(value).toISOString()});
    }
    try {
      await request(`/api/v1/campaigns/${id}/${action}`, {method: "POST", body});
      notice(`${action} completed.`); await refresh();
    } catch (error) {
      notice(error.message);
      if (error.problem?.blocks) renderBlocks(error.problem.blocks);
    }
  }));

  restoreToken();
  if (hasToken()) refresh();
})();
