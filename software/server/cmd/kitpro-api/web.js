(() => {
  function operationOutcome(status, result, transportFailed = false) {
    if (result && (result.id || result.status === "failed" || result.status === "action_required")) return "existing-operation";
    if (transportFailed) return "unknown";
    if (status === 401 || status === 403) return "not-submitted";
    return status >= 400 ? "unknown" : "accepted";
  }

  function friendlyError(status, raw, outcome, operationID = "") {
    const text = String(raw || "").toLowerCase();
    if (outcome === "existing-operation") {
      const identity = operationID ? ` ${operationID}` : "";
      return `The operation${identity} exists but needs attention. Review Activity and the application state before taking another action.`;
    }
    if (outcome === "unknown") return "KITPro could not confirm whether it created an operation. Check Activity and the application state before trying again.";
    if (status === 401) return "KITPro did not submit the operation because your session ended. Sign in and try again.";
    if (status === 403) return "KITPro did not submit the operation because the request was blocked. Refresh the page and try again.";
    if (text.includes("lan bind address")) return "Local network access is not configured for this server. Set its LAN address in KITPro's server configuration, then try again.";
    return "KITPro did not submit the operation. Review Technical details, then try again.";
  }

  function presentCredential(target, value) {
    target.textContent = String(value);
  }

  if (globalThis.KITPRO_TEST_MODE) {
    globalThis.KITPRO_OPERATION_TEST_API = { friendlyError, operationOutcome, presentCredential };
    return;
  }

  const panels = [...document.querySelectorAll("[data-view]")];
  const nav = [...document.querySelectorAll("[data-nav]")];
  const title = document.querySelector("[data-view-title]");
  const sidebar = document.querySelector(".sidebar");

  function showView() {
    const requested = location.hash.replace("#", "") || "dashboard";
    const active = panels.some((panel) => panel.dataset.view === requested) ? requested : "dashboard";
    panels.forEach((panel) => { panel.hidden = panel.dataset.view !== active; });
    nav.forEach((link) => {
      const selected = link.dataset.nav === active;
      link.classList.toggle("is-active", selected);
      if (selected) link.setAttribute("aria-current", "page"); else link.removeAttribute("aria-current");
    });
    if (title) title.textContent = active.charAt(0).toUpperCase() + active.slice(1);
    sidebar?.classList.remove("is-open");
  }

  window.addEventListener("hashchange", showView);
  document.querySelector("[data-nav-toggle]")?.addEventListener("click", (event) => {
    const open = sidebar?.classList.toggle("is-open") || false;
    event.currentTarget.setAttribute("aria-expanded", String(open));
  });
  showView();

  const banner = document.querySelector("[data-operation-banner]");
  function announce(titleText, body, tone = "progress") {
    if (!banner) return;
    banner.hidden = false;
    banner.dataset.tone = tone;
    banner.querySelector("strong").textContent = titleText;
    banner.querySelector("p").textContent = body;
    banner.querySelector(".progress-track").hidden = tone !== "progress";
  }

  document.querySelectorAll("form[data-async]").forEach((form) => {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector("button[type=submit]");
      const previous = button?.textContent || "";
      const card = form.closest(".installation-card");
      const buttons = card
        ? Array.from(card.querySelectorAll("form[data-async] button[type=submit]"))
        : Array.from(form.querySelectorAll("button[type=submit]"));
      buttons.forEach((candidate) => { candidate.disabled = true; });
      if (button) button.textContent = form.dataset.pending || "Working…";
      announce(form.dataset.pending || "Working…", form.dataset.progress || "KITPro is applying the requested change.");
      try {
        const response = await fetch(form.action, { method: form.method || "POST", body: new FormData(form), headers: { Accept: "application/json" } });
        const body = await response.text();
        let result = {};
        try { result = JSON.parse(body); } catch (_) { /* Some successful handlers have no response body. */ }
        const outcome = operationOutcome(response.status, result);
        if (!response.ok) throw Object.assign(new Error(body), { status: response.status, outcome, operationID: result.id || "" });
        if (result.status === "failed" || result.status === "action_required") {
          throw Object.assign(new Error("operation requires review"), { status: response.status, outcome: "existing-operation", operationID: result.id || "" });
        }
        announce(form.dataset.success || "Operation accepted", "KITPro will refresh this view with the latest state.", "success");
        window.setTimeout(() => location.reload(), 700);
      } catch (error) {
        const outcome = error.outcome || operationOutcome(error.status || 0, {}, !error.status);
        announce("Operation could not be completed", friendlyError(error.status || 0, error.message, outcome, error.operationID || ""), "danger");
        buttons.forEach((candidate) => { candidate.disabled = false; });
        if (button) button.textContent = previous;
      }
    });
  });

  document.querySelectorAll("form[data-credential-reveal]").forEach((form) => {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const container = form.closest("[data-credential]");
      const target = container?.querySelector("[data-credential-value]");
      const error = container?.querySelector("[data-credential-error]");
      const button = form.querySelector("button[type=submit]");
      if (!target || !button) return;
      button.disabled = true;
      if (error) error.hidden = true;
      try {
        const response = await fetch(form.action, { method: "POST", body: new FormData(form), headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("credential reveal rejected");
        const result = await response.json();
        if (typeof result.value !== "string" || !/^[a-f0-9]{64}$/.test(result.value)) throw new Error("invalid credential response");
        presentCredential(target, result.value);
        button.textContent = "Reveal again";
      } catch (_) {
        if (error) error.hidden = false;
      } finally {
        button.disabled = false;
      }
    });
  });

  document.querySelectorAll("[data-access-form]").forEach((form) => {
    form.querySelectorAll("input[type=radio]").forEach((radio) => radio.addEventListener("change", () => {
      const button = form.querySelector("button[type=submit]");
      if (button) button.disabled = false;
    }));
  });
})();
