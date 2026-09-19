(() => {
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

  function friendlyError(status, raw) {
    const text = String(raw || "").toLowerCase();
    if (status === 401) return "Your session has ended. Sign in and try again.";
    if (status === 403) return "KITPro blocked this request. Refresh the page and try again.";
    if (text.includes("lan bind address")) return "Local network access is not configured for this server. Set its LAN address in KITPro's server configuration, then try again.";
    if (status === 409 || text.includes("port")) return "KITPro could not apply that change because the requested network port is unavailable.";
    if (text.includes("docker") || text.includes("podman") || text.includes("runtime") || status === 503) return "Container services are unavailable right now. Check the container runtime and try again.";
    if (text.includes("image")) return "KITPro could not download the trusted application image. Check the server connection and retry.";
    return "KITPro could not complete this operation. You can safely retry or review Technical details.";
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
        if (!response.ok) throw Object.assign(new Error(body), { status: response.status });
        let result = {};
        try { result = JSON.parse(body); } catch (_) { /* Some successful handlers have no response body. */ }
        if (result.status === "failed") throw Object.assign(new Error("operation failed"), { status: 503 });
        announce(form.dataset.success || "Operation accepted", "KITPro will refresh this view with the latest state.", "success");
        window.setTimeout(() => location.reload(), 700);
      } catch (error) {
        announce("Operation could not be completed", friendlyError(error.status || 0, error.message), "danger");
        buttons.forEach((candidate) => { candidate.disabled = false; });
        if (button) button.textContent = previous;
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
