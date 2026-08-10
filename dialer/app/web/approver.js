(() => {
  const {request, saveToken, restoreToken, hasToken} =
    window.DialerAPI.client("approver", "#approver-token");
  const $ = selector => document.querySelector(selector);
  const textNode = text => {
    const item = document.createElement("div");
    item.className = "item";
    item.textContent = text;
    return item;
  };
  const notice = text => { $("#notice").textContent = text; };

  async function showCampaign() {
    const id = $("#campaign-select").value;
    if (!id) return;
    const detail = await request(`/api/v1/campaigns/${id}`);
    const campaign = detail.campaign;
    $("#campaign-detail").replaceChildren(
      textNode(`${campaign.name} — ${campaign.state}`),
      textNode(`Audio asset: ${campaign.message_asset_id || "missing"}`),
      textNode(`Caller ID evidence: ${campaign.caller_id_id || "missing"}`)
    );
    const blocks = $("#blocks");
    blocks.replaceChildren();
    (detail.safety_blocks || []).forEach(block => {
      const item = document.createElement("div");
      item.className = "block";
      item.textContent = `${block.code}: ${block.message}`;
      blocks.append(item);
    });
  }

  async function refresh() {
    try {
      const campaigns = await request("/api/v1/campaigns");
      const select = $("#campaign-select");
      const selected = select.value;
      select.replaceChildren();
      campaigns.filter(item => item.state === "VALIDATING").forEach(campaign => {
        const option = document.createElement("option");
        option.value = campaign.id;
        option.textContent = `${campaign.name} — ${campaign.state}`;
        select.append(option);
      });
      if ([...select.options].some(option => option.value === selected)) {
        select.value = selected;
      }
      await showCampaign();
      notice("Review queue refreshed.");
    } catch (error) {
      notice(error.message);
    }
  }

  $("#save-token").addEventListener("click", () => {
    saveToken();
    notice("Approver token saved in this tab.");
    refresh();
  });
  $("#refresh").addEventListener("click", refresh);
  $("#campaign-select").addEventListener("change", () => showCampaign().catch(error => notice(error.message)));
  $("#approve").addEventListener("click", async () => {
    const id = $("#campaign-select").value;
    if (!id || !confirm("Approve this campaign's exact audio and caller ID?")) return;
    try {
      await request(`/api/v1/campaigns/${id}/approve`, {method: "POST"});
      notice("Campaign approved.");
      await refresh();
    } catch (error) {
      notice(error.message);
    }
  });

  restoreToken();
  if (hasToken()) refresh();
})();
