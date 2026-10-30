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
  function presentHostText(target, value) {
    target.textContent = String(value ?? "");
  }

  function storageWarningTone(warnings) {
    if (!Array.isArray(warnings)) return "normal";
    if (warnings.includes("capacity_critical") || warnings.includes("mounted_read_only")) return "danger";
    return warnings.length ? "warning" : "normal";
  }

  function networkRate(value) {
    if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return "Collecting…";
    const units = ["B/s", "KiB/s", "MiB/s", "GiB/s"]; let amount = value; let unit = 0;
    while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit += 1; }
    return `${amount.toFixed(unit ? 1 : 0)} ${units[unit]}`;
  }
  function networkAddressLabel(address) {
    return address && address.address ? `${address.address}/${address.prefix_length}` : "";
  }


  function healthLabel(state) {
    return ({healthy:"Healthy",warning:"Warning",critical:"Critical",degraded:"Degraded",unavailable:"Unavailable",unsupported:"Unsupported",not_configured:"Not configured"})[state] || "Unavailable";
  }
  function healthTone(state) {
    if (state === "critical") return "danger";
    if (state === "warning" || state === "degraded") return "warning";
    if (state === "healthy") return "success";
    return "normal";
  }
  function usedPercent(available, total) {
    if (typeof available !== "number" || typeof total !== "number" || total <= 0 || available < 0 || available > total) return null;
    return 100 * (total - available) / total;
  }
  function serviceActionLabel(action) { return ({start:"Start",stop:"Stop",restart:"Restart",reload:"Reload",enable:"Enable",disable:"Disable"})[action] || action; }
  function serviceActionEffect(action) { return ({start:"The service will be started.",stop:"The service will stop and may interrupt users.",restart:"The service will be interrupted while systemd restarts it.",reload:"The service will reload its configuration.",enable:"The service will start automatically during future boots.",disable:"The service will no longer start automatically during future boots."})[action] || "The service state will change."; }

  if (globalThis.KITPRO_TEST_MODE) {
    globalThis.KITPRO_OPERATION_TEST_API = { friendlyError, operationOutcome, presentCredential, presentHostText, storageWarningTone, networkRate, networkAddressLabel, healthLabel, healthTone, usedPercent, serviceActionLabel, serviceActionEffect };
    return;
  }

  const panels = [...document.querySelectorAll("[data-view]")];
  const nav = [...document.querySelectorAll("[data-nav]")];
  const title = document.querySelector("[data-view-title]");
  const sidebar = document.querySelector(".sidebar");

  const dashboardPanel = document.querySelector("[data-dashboard-live]");
  const healthPanel = document.querySelector("[data-host-health]");
  let dashboardPollTimer = null;
  const healthDestinations = new Set(["#host-health","#host-overview","#host-services","#host-logs","#host-storage","#host-network","#applications","#settings"]);
  function findingCard(check) {
    const card=document.createElement("article");card.className="health-finding";card.dataset.tone=healthTone(check.state);
    const head=document.createElement("div");head.className="health-finding-head";const heading=document.createElement("h3");presentHostText(heading,check.summary||check.id);const badge=document.createElement("span");badge.className=`badge badge-${healthTone(check.state)}`;presentHostText(badge,healthLabel(check.state));head.append(heading,badge);card.append(head);
    const explanation=document.createElement("p");explanation.className="panel-description";presentHostText(explanation,check.explanation||"No additional explanation is available.");card.append(explanation);
    if(check.evidence){const evidence=document.createElement("p");evidence.className="health-evidence";presentHostText(evidence,check.evidence);card.append(evidence)}
    if(check.recommended_action){const action=document.createElement("p");action.className="health-action";presentHostText(action,check.recommended_action);card.append(action)}
    if(healthDestinations.has(check.destination)){const link=document.createElement("a");link.className="button button-small";link.href=check.destination;presentHostText(link,"Open related view");card.append(link)}
    return card;
  }
  function liveSummary(items,label){const strip=document.createElement("div");strip.className="summary-strip health-summary";strip.setAttribute("aria-label",label);items.forEach(([value,caption])=>strip.append(storageMetric(value,caption)));return strip}
  function componentCard(value){const card=hostCard((value.component||"component").replaceAll("_"," "),[["State",healthLabel(value.state)],["Critical",value.critical??0],["Warnings",value.warning??0],["Degraded",value.degraded??0],["Unavailable",value.unavailable??0],["Healthy",value.healthy??0]]);card.dataset.tone=healthTone(value.state);return card}
  async function loadHealth(){
    if(!healthPanel)return;hostNotice(healthPanel,"Evaluating system health","Collecting bounded observations from Host Administration providers.");
    try{const response=await fetch("/api/v1/host/health",{cache:"no-store"});if(!response.ok)throw{status:response.status};const data=await response.json();const summary=data.summary||{};
      const overview=liveSummary([[healthLabel(summary.state),"Overall state"],[summary.critical??0,"Critical"],[summary.warnings??0,"Warnings"],[summary.unavailable??0,"Unavailable"]],"System health summary");
      const freshness=hostCard("Observation freshness",[["Evaluated",data.evaluated_at],["Fresh until",data.fresh_until],["History","Current bounded snapshot; no monitoring database is stored."]]);
      const components=document.createElement("section");components.className="section-block";const ct=document.createElement("h2");presentHostText(ct,"Component health");const cg=document.createElement("div");cg.className="health-component-grid";Object.values(data.components||{}).sort((a,b)=>String(a.component).localeCompare(String(b.component))).forEach(item=>cg.append(componentCard(item)));components.append(ct,cg);
      const findings=document.createElement("section");findings.className="section-block";const ft=document.createElement("h2");presentHostText(ft,"Findings");const fg=document.createElement("div");fg.className="health-findings";(data.checks||[]).forEach(check=>fg.append(findingCard(check)));findings.append(ft,fg);
      healthPanel.replaceChildren(overview,freshness,components,findings);
    }catch(error){hostNotice(healthPanel,"System Health unavailable",hostFailure(error.status,"health"))}
  }
  function dashboardHero(data){
    const state=data.health?.state||"unavailable";const hero=document.createElement("section");hero.className=`health-hero health-live is-${state}`;const copy=document.createElement("div");copy.className="health-copy";const status=document.createElement("div");status.className="health-state";const dot=document.createElement("span");dot.className="status-dot";const label=document.createElement("span");presentHostText(label,healthLabel(state));status.append(dot,label);const heading=document.createElement("h2");presentHostText(heading,state==="healthy"?"Your server is healthy.":state==="critical"?"Your server needs immediate attention.":"Review the current server findings.");const detail=document.createElement("p");presentHostText(detail,`${data.health?.critical??0} critical / ${data.health?.warnings??0} warnings / ${data.health?.unavailable??0} unavailable`);copy.append(status,heading,detail);
    const metrics=document.createElement("div");metrics.className="health-metrics";[["Hostname",data.host?.hostname||"Unavailable"],["Operating system",data.host?.os||"Unavailable"],["Evaluated",data.evaluated_at||"Unavailable"]].forEach(([name,value])=>{const item=document.createElement("div");item.className="health-metric";const key=document.createElement("span");key.className="metric-label";presentHostText(key,name);const val=document.createElement("span");val.className="metric-value";presentHostText(val,value);item.append(key,val);metrics.append(item)});hero.append(copy,metrics);return hero
  }
  function dashboardCard(title,rows,destination){const card=hostCard(title,rows);card.classList.add("dashboard-card");const link=document.createElement("a");link.className="button button-small";link.href=destination;presentHostText(link,"View details");card.append(link);return card}
  async function loadDashboard(){
    if(!dashboardPanel)return;
    try{const response=await fetch("/api/v1/dashboard",{cache:"no-store"});if(!response.ok)throw{status:response.status};const data=await response.json();const memoryUsed=usedPercent(data.resources?.memory?.available_bytes,data.resources?.memory?.total_bytes);
      const resources=liveSummary([[data.resources?.cpu_utilization_percent==null?"Unavailable":`${data.resources.cpu_utilization_percent.toFixed(1)}%`,"CPU"],[memoryUsed==null?"Unavailable":`${memoryUsed.toFixed(1)}%`,"Memory used"],[data.storage?.root_usage_percent==null?"Unavailable":`${data.storage.root_usage_percent.toFixed(1)}%`,"Root filesystem"],[networkRate(data.resources?.rx_bytes_per_second),"Network receive"]],"Current resources");
      const cards=document.createElement("div");cards.className="dashboard-grid";cards.append(
        dashboardCard("Applications",[["Installed",data.applications?.installed],["Running",data.applications?.running],["Stopped",data.applications?.stopped],["Failed",data.applications?.failed],["Readiness failures",data.applications?.readiness_failures]],"#applications"),
        dashboardCard("Services",[["Total",data.services?.total],["Failed",data.services?.failed],["KITPro API",data.services?.essential?.["kitpro-api.service"]],["Docker",data.services?.essential?.["docker.service"]]],"#host-services"),
        dashboardCard("Storage",[["Available",hostBytes(data.storage?.available_bytes)],["Used",hostBytes(data.storage?.used_bytes)],["Mounted",data.storage?.mounted_filesystems],["Warnings",data.storage?.warnings]],"#host-storage"),
        dashboardCard("Networking",[["State",healthLabel(data.network?.health)],["Active interfaces",data.network?.active_interfaces],["Default routes",data.network?.default_routes],["Resolvers",data.network?.resolvers],["Transmit",networkRate(data.network?.tx_bytes_per_second)]],"#host-network"));
      const attention=document.createElement("section");attention.className="section-block";const title=document.createElement("h2");presentHostText(title,"Attention required");const findings=document.createElement("div");findings.className="health-findings dashboard-findings";if(data.attention?.length){data.attention.forEach(item=>findings.append(findingCard(item)))}else{hostNotice(findings,"No attention required","Current observations contain no actionable findings.")}attention.append(title,findings);
      dashboardPanel.replaceChildren(dashboardHero(data),resources,cards,attention);document.querySelectorAll("[data-dashboard-fallback]").forEach(item=>{item.hidden=true});
    }catch(error){hostNotice(dashboardPanel,"Dashboard unavailable",hostFailure(error.status,"dashboard"))}
  }
  function startDashboardPolling(){if(dashboardPollTimer)clearInterval(dashboardPollTimer);dashboardPollTimer=setInterval(()=>{if(!document.hidden&&(location.hash===""||location.hash==="#dashboard"))loadDashboard()},15000)}
  function stopDashboardPolling(){if(dashboardPollTimer){clearInterval(dashboardPollTimer);dashboardPollTimer=null}}


  function showView() {
    const requested = location.hash.replace("#", "") || "dashboard";
    const active = panels.some((panel) => panel.dataset.view === requested) ? requested : "dashboard";
    panels.forEach((panel) => { panel.hidden = panel.dataset.view !== active; });
    nav.forEach((link) => {
      const selected = link.dataset.nav === active;
      link.classList.toggle("is-active", selected);
      if (selected) link.setAttribute("aria-current", "page"); else link.removeAttribute("aria-current");
    });
    const viewTitles = {"host-health":"System Health","host-overview":"Host Overview","host-services":"Services","host-logs":"Logs","host-storage":"Storage","host-network":"Networking","activity":"Activity","applications":"Applications","settings":"Settings","dashboard":"Dashboard"};
    if (title) title.textContent = viewTitles[active] || active.charAt(0).toUpperCase() + active.slice(1);
    sidebar?.classList.remove("is-open");
  }

  const hostPanel = document.querySelector("[data-host-overview]");
  const hostText = (value) => value === undefined || value === null || value === "" ? "Unavailable" : String(value);
  const hostBytes = (value) => typeof value === "number" ? `${(value / (1024 ** 3)).toFixed(1)} GiB` : "Unavailable";
  function hostRow(list, label, value) {
    const row = document.createElement("div");
    const term = document.createElement("dt"); term.textContent = label;
    const detail = document.createElement("dd"); detail.textContent = hostText(value);
    row.append(term, detail); list.append(row);
  }
  function hostCard(title, rows) {
    const card = document.createElement("section"); card.className = "settings-panel";
    const heading = document.createElement("h2"); heading.textContent = title; card.append(heading);
    const list = document.createElement("dl"); list.className = "info-list";
    rows.forEach(([label, value]) => hostRow(list, label, value)); card.append(list);
    return card;
  }
  async function loadHostOverview() {
    if (!hostPanel) return;
    try {
      const response = await fetch("/api/v1/host/overview", {cache:"no-store"});
      if (!response.ok) throw new Error("Host information is unavailable.");
      const data = await response.json();
      const memory = data.memory;
      const disk = data.root_disk;
      const interfaces = Array.isArray(data.interfaces) ? data.interfaces.map((item) => `${item.name}: ${item.up ? "up" : "down"}${item.addresses?.length ? ` (${item.addresses.join(", ")})` : ""}`).join("; ") : null;
      hostPanel.replaceChildren(
        hostCard("System", [["KITPro Server", data.server_version], ["KITPro Linux", data.kitpro_linux ? `${data.kitpro_linux.release} · ${data.kitpro_linux.generation}` : "Not installed or metadata unavailable"], ["Hostname", data.hostname], ["Operating system", data.os], ["Kernel", data.kernel], ["Architecture", data.architecture], ["CPU model", data.cpu_model], ["CPU utilization", data.cpu_utilization_percent == null ? null : `${data.cpu_utilization_percent.toFixed(1)}%`], ["Uptime", data.uptime_seconds == null ? null : `${Math.floor(data.uptime_seconds / 3600)} hours`], ["Load (1, 5, 15 min)", data.load_average?.join(" / ")]]),
        hostCard("Resources", [["Memory available / total", memory ? `${hostBytes(memory.available_bytes)} / ${hostBytes(memory.total_bytes)}` : null], ["Swap free / total", memory ? `${hostBytes(memory.swap_free_bytes)} / ${hostBytes(memory.swap_total_bytes)}` : null], ["Root filesystem available / total", disk ? `${hostBytes(disk.available_bytes)} / ${hostBytes(disk.total_bytes)}` : null]]),
        hostCard("Host facilities", [["Network interfaces", interfaces], ["systemd", data.systemd?.detail], ["Security", data.security?.detail], ["Collector status", Object.entries(data.collectors || {}).filter(([, status]) => status.state !== "available").map(([name, status]) => `${name}: ${status.detail || status.state}`).join("; ") || "All overview collectors available"]])
      );
    } catch (_) {
      hostPanel.replaceChildren(hostCard("Host information unavailable", [["Status", "The host overview could not be loaded. Try refreshing the page."]]));
    }
  }
  document.querySelector('[data-nav="host-overview"]')?.addEventListener("click", loadHostOverview);
  if (location.hash === "#host-overview") loadHostOverview();

  const serviceForm = document.querySelector("[data-host-services-form]");
  const serviceResults = document.querySelector("[data-host-services-results]");
  const serviceDetail = document.querySelector("[data-host-service-detail]");
  const servicePager = document.querySelector("[data-host-services-pager]");
  const logsForm = document.querySelector("[data-host-logs-form]");
  const logsResults = document.querySelector("[data-host-logs-results]");
  const logsPager = document.querySelector("[data-host-logs-pager]");
  const storagePanel = document.querySelector("[data-host-storage]");
  const networkPanel = document.querySelector("[data-host-network]");
  const networkDiagnosticForm = document.querySelector("[data-host-network-diagnostics]");
  const networkDiagnosticResult = document.querySelector("[data-host-network-diagnostic-result]");
  let networkPollTimer = null;
  let serviceOffset = null;
  let serviceRequest = 0;
  let logCursor = "";
  function hostNotice(target, heading, detail) {
    if (!target) return;
    const card = document.createElement("section"); card.className = "settings-panel";
    const title = document.createElement("h2"); presentHostText(title, heading);
    const body = document.createElement("p"); body.className = "panel-description"; presentHostText(body, detail);
    card.append(title, body); target.replaceChildren(card);
  }
  function hostFailure(status, kind) {
    if (status === 401) return "Your session ended. Sign in again.";
    if (status === 403) return kind === "logs" ? "Journal access is not enabled or permitted for this server." : "Access denied.";
    if (status === 501) return "This host does not support this capability.";
    if (status === 400) return "The query is invalid. Check the filters and try again.";
    return "The host data source is unavailable. Try again later.";
  }
  function adminTable(className, captionText, headings) {
    const wrapper = document.createElement("div"); wrapper.className = "admin-table-wrap";
    const table = document.createElement("table"); table.className = `admin-table ${className}`;
    const caption = document.createElement("caption"); caption.className = "visually-hidden"; presentHostText(caption, captionText);
    const head = document.createElement("thead"); const row = document.createElement("tr");
    for (const heading of headings) { const cell = document.createElement("th"); cell.scope = "col"; presentHostText(cell, heading); row.append(cell); }
    const body = document.createElement("tbody"); head.append(row); table.append(caption, head, body); wrapper.append(table);
    return {wrapper, body};
  }
  function tableCell(value, className = "") { const cell = document.createElement("td"); cell.className = className; presentHostText(cell, value); return cell; }
  function statusBadge(value) {
    const state = String(value || "unknown").toLowerCase(); const badge = document.createElement("span");
    badge.className = `badge ${state === "active" ? "badge-success" : state === "failed" ? "badge-danger" : "badge-plain"}`; presentHostText(badge, state);
    return badge;
  }
  function managementBadge(item) {
    const state = item.management_state || "Read only"; const category = String(item.management_category || "unknown").replaceAll("_", " ");
    const badge = document.createElement("span"); badge.className = `badge ${item.managed ? "badge-success" : category.includes("protected") || state.toLowerCase().includes("protected") ? "badge-warning" : "badge-plain"}`;
    presentHostText(badge, state); badge.title = category; return badge;
  }
  function serviceRow(item) {
    const row = document.createElement("tr"); row.tabIndex = 0;
    const name = tableCell(item.name, "service-name mono");
    const description = tableCell(item.description || "No description", "service-description"); description.title = item.description || "No description";
    const status = document.createElement("td"); status.append(statusBadge(item.active_state));
    const startup = tableCell(item.enabled_state || "unknown", "service-startup");
    const pid = tableCell(item.main_pid || "—", "service-pid mono");
    const management = document.createElement("td"); management.append(managementBadge(item));
    const actions = document.createElement("td"); actions.className = "table-actions";
    const menu = document.createElement("select"); menu.className = "row-action-select"; menu.setAttribute("aria-label", `Actions for ${item.name}`);
    for (const [value, label] of [["", "Actions"], ["details", "Details"], ["logs", "View logs"]]) { const option = document.createElement("option"); option.value = value; presentHostText(option, label); menu.append(option); }
    const openDetails = () => { serviceResults.querySelectorAll("tr[aria-selected]").forEach(selected => selected.removeAttribute("aria-selected")); row.setAttribute("aria-selected", "true"); loadServiceDetail(item.name); };
    menu.addEventListener("change", () => { const choice = menu.value; menu.value = ""; if (choice === "details") openDetails(); if (choice === "logs") { logsForm.elements.unit.value = item.name; location.hash = "#host-logs"; loadHostLogs(false); } });
    actions.append(menu);
    row.addEventListener("dblclick", openDetails);
    row.addEventListener("keydown", event => { if (event.key === "Enter" && event.target === row) openDetails(); });
    row.append(name, description, status, startup, pid, management, actions); return row;
  }
  async function loadServices(more = false) {
    if (!serviceResults || !serviceForm) return;
    const request = ++serviceRequest;
    if (!more) { serviceOffset = null; serviceDetail?.replaceChildren(); hostNotice(serviceResults, "Loading services", "Reading systemd service inventory."); }
    servicePager.hidden = true;
    const params = new URLSearchParams({search:serviceForm.elements.search.value, state:serviceForm.elements.state.value, limit:"25", offset:String(more ? serviceOffset ?? 0 : 0)});
    try {
      const response = await fetch(`/api/v1/host/services?${params}`, {cache:"no-store"});
      if (!response.ok) throw {status:response.status};
      const page = await response.json();
      if (request !== serviceRequest) return;
      if (!more) serviceResults.replaceChildren();
      if (!page.items?.length && !more) hostNotice(serviceResults, "No services found", "No service matches these filters.");
      if (page.items?.length) {
        let body = serviceResults.querySelector(".services-table tbody");
        if (!body) { const table = adminTable("services-table", "System services", ["Service", "Description", "Status", "Startup", "PID", "Management", "Actions"]); serviceResults.append(table.wrapper); body = table.body; }
        for (const item of page.items) body.append(serviceRow(item));
      }
      serviceOffset = typeof page.next_offset === "number" ? page.next_offset : null;
      servicePager.hidden = serviceOffset === null;
    } catch (error) { if (request === serviceRequest) hostNotice(serviceResults, "Services unavailable", hostFailure(error.status, "services")); }
  }
  async function loadServiceDetail(name) {
    hostNotice(serviceDetail, "Loading service", name);
    try {
      const response = await fetch(`/api/v1/host/services/${encodeURIComponent(name)}`, {cache:"no-store"});
      if (!response.ok) throw {status:response.status};
      const item = await response.json();
      const card=hostCard(`${item.name} details`, [["Description", item.description], ["Canonical unit", item.canonical_unit], ["Management", item.management_state], ["Category", String(item.management_category||"unknown").replaceAll("_"," ")], ["Unit definition", item.unit_file || "Unavailable"], ["Trusted definition", item.unit_file_trusted ? "Yes" : "No"], ["Loaded", item.loaded_state], ["Active", item.active_state], ["Substate", item.sub_state], ["Enabled", item.enabled_state], ["Main PID", item.main_pid || "Unavailable"], ["Failure", item.failure || "None reported"], ["Dependencies", item.dependencies?.join("; ") || "None reported"]]);
      const actions=document.createElement("div");actions.className="service-actions";actions.setAttribute("aria-label",`Actions for ${item.name}`);
      for(const eligibility of item.actions||[]){const button=document.createElement("button");button.type="button";button.className=`button button-small${["stop","disable"].includes(eligibility.action)?" button-danger":""}`;presentHostText(button,serviceActionLabel(eligibility.action));button.disabled=!eligibility.allowed;if(!eligibility.allowed)button.title=eligibility.reason||"This action is unavailable.";button.addEventListener("click",()=>confirmServiceAction(item,eligibility.action));actions.append(button)}
      const blocked=(item.actions||[]).filter(action=>!action.allowed);if(item.protection_reason||blocked.length){const notice=document.createElement("p");notice.className="service-protection";presentHostText(notice,item.protection_reason||blocked[0].reason||"Some actions are unavailable under the host protection policy.");card.append(notice)}
      const activity=document.createElement("a");activity.className="button button-small";activity.href="#activity";presentHostText(activity,"View all activity");actions.append(activity);card.append(actions);
      const history=document.createElement("section");history.className="settings-panel service-history";const historyTitle=document.createElement("h2");presentHostText(historyTitle,"Service activity");const historyFilters=document.createElement("form");historyFilters.className="filter-form";const actionFilter=document.createElement("select");actionFilter.name="action";actionFilter.setAttribute("aria-label","Filter service activity by action");for(const value of ["","start","stop","restart","reload","enable","disable"]){const option=document.createElement("option");option.value=value;presentHostText(option,value?serviceActionLabel(value):"All actions");actionFilter.append(option)}const resultFilter=document.createElement("select");resultFilter.name="result";resultFilter.setAttribute("aria-label","Filter service activity by result");for(const value of ["","completed","rejected","failed","timed_out","outcome_unknown"]){const option=document.createElement("option");option.value=value;presentHostText(option,value?value.replaceAll("_"," "):"All results");resultFilter.append(option)}const apply=document.createElement("button");apply.type="submit";apply.className="button button-small";presentHostText(apply,"Filter activity");historyFilters.append(actionFilter,resultFilter,apply);const historyResults=document.createElement("div");historyResults.className="service-history-results";history.append(historyTitle,historyFilters,historyResults);serviceDetail.replaceChildren(card,history);historyFilters.addEventListener("submit",event=>{event.preventDefault();loadServiceHistory(item.name,historyResults,historyFilters,0,false)});loadServiceHistory(item.name,historyResults,historyFilters,0,false);
    } catch (error) { hostNotice(serviceDetail, "Service details unavailable", hostFailure(error.status, "services")); }
  }
  async function loadServiceHistory(name,container,filters,offset=0,append=false){
    if(!append)hostNotice(container,"Loading service activity",`Reading bounded operation history for ${name}.`);
    const params=new URLSearchParams({limit:"20",offset:String(offset)});const action=filters?.elements.action?.value||"";const result=filters?.elements.result?.value||"";if(action)params.set("action",action);if(result)params.set("result",result);
    try{const response=await fetch(`/api/v1/host/services/${encodeURIComponent(name)}/history?${params}`,{cache:"no-store"});if(!response.ok)throw{status:response.status};const page=await response.json();if(!append)container.replaceChildren();if(!page.items?.length&&!append){hostNotice(container,"No service activity","No authorized operation records match these filters.");return}let body=container.querySelector(".service-history-table tbody");if(!body){const table=adminTable("service-history-table","Service operation history",["Time","Administrator","Action","Result","State change","Failure"]);container.append(table.wrapper);body=table.body}for(const item of page.items||[]){const row=document.createElement("tr");const time=tableCell(item.timestamp,"mono history-time");const administrator=tableCell(item.administrator,"history-administrator");const action=tableCell(serviceActionLabel(item.action),"history-action");const result=document.createElement("td");result.append(statusBadge(item.status));const transition=tableCell(`${item.previous_state||"unknown"} → ${item.resulting_state||"unknown"}`,"history-transition");transition.title=`Authorization: ${item.authorization_outcome||"unknown"}; policy: ${item.policy_outcome||"unknown"}; systemd: ${item.systemd_job_outcome||"unknown"}; operation: ${item.operation_id||"unknown"}`;const failure=tableCell(item.failure_reason||"—","history-failure");failure.title=item.failure_reason||"No failure reported";row.append(time,administrator,action,result,transition,failure);body.append(row)}if(page.next_offset!=null){const more=document.createElement("button");more.type="button";more.className="button button-small";presentHostText(more,"Load more activity");more.addEventListener("click",()=>{more.remove();loadServiceHistory(name,container,filters,page.next_offset,true)});container.append(more)}}catch(error){if(!append)hostNotice(container,"Service activity unavailable",hostFailure(error.status,"services"))}
  }
  async function confirmServiceAction(item,action){
    let preview;try{const response=await fetch(`/api/v1/host/services/${encodeURIComponent(item.name)}/actions/${encodeURIComponent(action)}/preview`,{cache:"no-store"});if(!response.ok)throw {status:response.status};preview=await response.json()}catch(error){hostNotice(serviceDetail,"Preview unavailable",hostFailure(error.status,"services"));return}
    if(!preview.eligibility?.allowed){hostNotice(serviceDetail,"Operation not eligible",preview.eligibility?.reason||"The current policy denied this operation.");return}
    const dialog=document.createElement("dialog");dialog.className="service-action-dialog";const form=document.createElement("form");form.method="dialog";const title=document.createElement("h2");presentHostText(title,`${serviceActionLabel(action)} ${item.name}`);const effect=document.createElement("p");effect.className="panel-description";presentHostText(effect,serviceActionEffect(action));const warning=document.createElement("p");warning.className="storage-note";const concerns=preview.eligibility?.dependency_concerns?.join(", ");presentHostText(warning,concerns?`Dependency concerns: ${concerns}`:`Expected result: ${preview.eligibility?.expected_transition||"systemd state change"}. KITPro will recheck policy immediately before dispatch.`);form.append(title,effect,warning);
    let typed=null;if(["stop","disable"].includes(action)){const label=document.createElement("label");label.className="field-label";presentHostText(label,`Type ${item.name} to confirm`);typed=document.createElement("input");typed.required=true;typed.autocomplete="off";label.append(typed);form.append(label)}
    const controls=document.createElement("div");controls.className="service-actions";const cancel=document.createElement("button");cancel.type="button";cancel.className="button";presentHostText(cancel,"Cancel");cancel.addEventListener("click",()=>dialog.close());const submit=document.createElement("button");submit.type="button";submit.className=`button button-primary${["stop","disable"].includes(action)?" button-danger":""}`;presentHostText(submit,`Confirm ${serviceActionLabel(action).toLowerCase()}`);submit.addEventListener("click",async()=>{if(typed&&typed.value!==item.name){typed.setCustomValidity(`Type ${item.name} exactly.`);typed.reportValidity();return}submit.disabled=true;presentHostText(submit,"Operation running…");await runServiceAction(item.name,action,dialog)});controls.append(cancel,submit);form.append(controls);dialog.append(form);document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal();
  }
  async function runServiceAction(name,action,dialog){
    const csrf=document.querySelector('input[name="csrf_token"]')?.value||"";
    try{const response=await fetch(`/api/v1/host/services/${encodeURIComponent(name)}/actions`,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify({action,confirmation_unit:name})});const result=await response.json().catch(()=>({}));if(!response.ok)throw{status:response.status,result};dialog.close();hostNotice(serviceDetail,result.status==="accepted"?"Operation accepted":"Operation completed",result.id?`${serviceActionLabel(action)} operation ${result.id}: ${result.status}.`:`${serviceActionLabel(action)} completed.`);await loadServices(false);await loadServiceDetail(name);loadHealth();loadDashboard()}catch(error){dialog.close();const operationID=error.result?.id||"";hostNotice(serviceDetail,"Service operation failed",friendlyError(error.status,error.result?.error,operationOutcome(error.status,error.result||{}),operationID))}
  }
  function journalPriority(value) { return ["Emergency", "Alert", "Critical", "Error", "Warning", "Notice", "Info", "Debug"][Number(value)] || "Unknown"; }
  function logRow(entry) {
    const row = document.createElement("tr");
    const timestamp = document.createElement("time"); timestamp.dateTime = entry.timestamp; timestamp.title = entry.timestamp;
    const parsed = new Date(entry.timestamp); presentHostText(timestamp, Number.isNaN(parsed.valueOf()) ? entry.timestamp : parsed.toLocaleString());
    const timeCell = document.createElement("td"); timeCell.className = "log-time mono"; timeCell.append(timestamp);
    const severityCell = document.createElement("td"); const severity = document.createElement("span");
    const priority = Number(entry.priority); severity.className = `log-severity log-severity-${Number.isInteger(priority) ? priority : "unknown"}`; presentHostText(severity, journalPriority(priority)); severityCell.append(severity);
    const source = tableCell(entry.unit || entry.identifier || "System", "log-source mono");
    const messageCell = document.createElement("td"); messageCell.className = "log-message-cell";
    const details = document.createElement("details"); details.className = "log-message";
    const summary = document.createElement("summary"); presentHostText(summary, entry.message || "(empty message)");
    const metadata = document.createElement("dl"); metadata.className = "log-metadata";
    [["Timestamp", entry.timestamp], ["Unit", entry.unit || "Not reported"], ["Identifier", entry.identifier || "Not reported"], ["Priority", `${journalPriority(priority)} (${entry.priority})`]].forEach(([label, value]) => hostRow(metadata, label, value));
    details.append(summary, metadata); messageCell.append(details); row.append(timeCell, severityCell, source, messageCell); return row;
  }
  async function loadHostLogs(more = false) {
    if (!logsForm || !logsResults) return;
    if (!more) {logCursor = "";hostNotice(logsResults, "Loading logs", "Reading recent system journal entries.");}
    logsPager.hidden = true;
    const params = new URLSearchParams({unit:logsForm.elements.unit.value, priority:logsForm.elements.priority.value, search:logsForm.elements.search.value, limit:"50"});
    for (const key of ["since", "until"]) {const value = logsForm.elements[key].value;if (value) params.set(key,new Date(value).toISOString());}
    if (more && logCursor) params.set("cursor",logCursor);
    try {
      const response = await fetch(`/api/v1/host/logs?${params}`, {cache:"no-store"});
      if (!response.ok) throw {status:response.status};
      const page = await response.json();
      if (!more) logsResults.replaceChildren();
      if (!page.entries?.length && !more) hostNotice(logsResults, "No logs found", "No journal entries match these filters.");
      if (page.entries?.length) {
        let body = logsResults.querySelector(".logs-table tbody");
        if (!body) { const table = adminTable("logs-table", "System journal entries", ["Timestamp", "Severity", "Unit / source", "Message"]); logsResults.append(table.wrapper); body = table.body; }
        for (const entry of page.entries) body.append(logRow(entry));
      }
      logCursor = page.next_cursor || "";logsPager.hidden = !logCursor;
    } catch (error) {hostNotice(logsResults, "Logs unavailable", hostFailure(error.status, "logs"));}
  }
  function storageMetric(value, label) {
    const item = document.createElement("div"); item.className = "summary-item";
    const amount = document.createElement("span"); amount.className = "summary-value"; presentHostText(amount, value);
    const caption = document.createElement("span"); caption.className = "summary-label"; presentHostText(caption, label);
    item.append(amount, caption); return item;
  }
  function storageDeviceRow(item) {
    const status = item.status?.state === "available" ? (item.mounted ? "Mounted" : "Not mounted") : item.status?.detail || item.status?.state;
    const row=document.createElement("tr");const device=tableCell(item.path||item.name,"mono storage-device");device.title=`Identifier: ${item.id||"unknown"}; parent: ${item.parent_id||"none"}`;const model=tableCell([item.vendor,item.model].filter(Boolean).join(" ")||"Not reported","storage-model");model.title=model.textContent;const type=tableCell(`${item.category||"unknown"} · ${item.device_type||"unknown"}`);const capacity=tableCell(hostBytes(item.size_bytes),"mono");const partitions=tableCell(item.partition_count??0);const mounts=tableCell(item.mount_points?.join(", ")||"—","storage-mounts");mounts.title=item.mount_points?.join(", ")||"Not mounted";const state=tableCell(status);state.title=`Transport: ${item.transport||"unknown"}; filesystem: ${item.filesystem_type||"unknown"}; protected: ${item.protection?.join(", ")||"none identified"}`;row.append(device,model,type,capacity,partitions,mounts,state);return row;
  }
  function storageFilesystemRow(item) {
    const usage = item.usage_percent == null ? "Unavailable" : `${item.usage_percent.toFixed(1)}%`;
    const row=document.createElement("tr");row.dataset.tone=storageWarningTone(item.warnings);const source=tableCell(item.source,"mono storage-source");source.title=`UUID: ${item.uuid||"unavailable"}; label: ${item.label||"none"}`;const filesystem=tableCell(item.type);const mount=tableCell(item.mount_point,"mono storage-mount");mount.title=`Root: ${item.mount_root||"/"}; protected: ${item.protection?.join(", ")||"none"}`;const total=tableCell(hostBytes(item.total_bytes),"mono");const used=tableCell(item.used_bytes==null?"Unavailable":hostBytes(item.used_bytes),"mono");const available=tableCell(hostBytes(item.available_bytes),"mono");const use=tableCell(usage);use.title=item.warnings?.join(", ")||"No warnings";const mode=tableCell(item.read_only?"Read only":"Read/write");row.append(source,filesystem,mount,total,used,available,use,mode);return row;
  }
  function storagePersistentRow(item){const row=document.createElement("tr");row.append(tableCell(item.source,"mono storage-source"),tableCell(item.target,"mono storage-mount"),tableCell(item.filesystem_type),tableCell(item.options?.join(", ")||"defaults"));return row}
  function storageTableSection(title, placeholder, items, renderer, headings, className, emptyText) {
    const section = document.createElement("section"); section.className = "section-block";
    const heading = document.createElement("div"); heading.className = "section-heading";
    const copy = document.createElement("div"); const h2 = document.createElement("h2"); presentHostText(h2,title); copy.append(h2);
    const input = document.createElement("input"); input.className = "storage-search"; input.type = "search"; input.placeholder = placeholder; input.setAttribute("aria-label",`Filter ${title.toLowerCase()}`);
    heading.append(copy,input);const region=document.createElement("div");region.className="admin-table-region";
    const render=()=>{const query=input.value.trim().toLowerCase();const matches=items.filter(item=>JSON.stringify(item).toLowerCase().includes(query));region.replaceChildren();if(!matches.length){hostNotice(region,`No ${title.toLowerCase()} found`,emptyText);return}const table=adminTable(className,title,headings);matches.forEach(item=>table.body.append(renderer(item)));region.append(table.wrapper)};
    input.addEventListener("input",render);render();section.append(heading,region);return section;
  }
  function storageManagementSection(items){
    const section=document.createElement("section");section.className="section-block";const heading=document.createElement("div");heading.className="section-heading";const copy=document.createElement("div");const title=document.createElement("h2");presentHostText(title,"Approved mount operations");const description=document.createElement("p");description.className="panel-description";presentHostText(description,"Only exact resources from the root-owned storage policy can be changed.");copy.append(title,description);heading.append(copy);section.append(heading);
    if(!items.length){const empty=document.createElement("div");hostNotice(empty,"Storage management unavailable","No reviewed mount resources are configured.");section.append(empty);return section}
    const table=adminTable("storage-management-table","Approved storage operations",["Resource","Source","Target","Filesystem","State","Persistence","Policy","Action"]);for(const item of items){const row=document.createElement("tr");const resource=tableCell(item.resource_id,"mono");const source=tableCell(item.source,"mono storage-source");const target=tableCell(item.target,"mono storage-mount");const filesystem=tableCell(item.filesystem_type);const state=tableCell(item.mounted?"Mounted":"Unmounted");const persistence=tableCell(item.persistence?`${item.persistence.state} · ${item.persistence.health}`:"Not configured");const policy=document.createElement("td");policy.append(statusBadge(item.eligibility?.allowed?"active":item.eligibility?.state||"denied"));policy.title=item.eligibility?.reason||"No policy explanation";const action=document.createElement("td");action.className="table-actions storage-row-actions";const button=document.createElement("button");button.type="button";button.className=`button button-small${(item.action==="unmount"||item.action==="disable_persistence")?" button-danger":""}`;presentHostText(button,storageActionLabel(item.action));button.disabled=!item.eligibility?.allowed;button.title=item.eligibility?.reason||"Preview this operation";button.addEventListener("click",()=>confirmStorageAction(item));const history=document.createElement("button");history.type="button";history.className="button button-small";presentHostText(history,"Activity");history.addEventListener("click",()=>showStorageHistory(item.resource_id));action.append(button,history);row.append(resource,source,target,filesystem,state,persistence,policy,action);table.body.append(row)}section.append(table.wrapper);return section
  }
  async function showStorageHistory(resourceID){
    const dialog=document.createElement("dialog");dialog.className="service-action-dialog storage-history-dialog";const title=document.createElement("h2");presentHostText(title,`${resourceID} operation history`);const content=document.createElement("div");hostNotice(content,"Loading storage activity","Reading bounded operation history.");const close=document.createElement("button");close.type="button";close.className="button";presentHostText(close,"Close");close.addEventListener("click",()=>dialog.close());dialog.append(title,content,close);document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal();
    try{const response=await fetch(`/api/v1/host/storage/${encodeURIComponent(resourceID)}/history?limit=20`,{cache:"no-store"});if(!response.ok)throw{status:response.status};const page=await response.json();content.replaceChildren();if(!page.items?.length){hostNotice(content,"No storage activity","No authorized mount operations have been recorded for this resource.");return}const table=adminTable("storage-history-table","Storage operation history",["Time","Administrator","Action","Result","State change","Failure"]);for(const item of page.items){const row=document.createElement("tr");const result=document.createElement("td");result.append(statusBadge(item.status));row.append(tableCell(item.timestamp,"mono"),tableCell(item.administrator),tableCell(item.action),result,tableCell(`${item.previous_state||"unknown"} → ${item.resulting_state||"unknown"}`),tableCell(item.failure_reason||"—"));table.body.append(row)}content.append(table.wrapper)}catch(error){hostNotice(content,"Storage activity unavailable",hostFailure(error.status,"storage"))}
  }
  function storageActionLabel(action){return {mount:"Mount",unmount:"Unmount",enable_persistence:"Enable persistence",disable_persistence:"Disable persistence",recover_persistence:"Recover configuration"}[action]||"Storage action"}
  async function confirmStorageAction(item){
    let preview;try{const response=await fetch(`/api/v1/host/storage/${encodeURIComponent(item.resource_id)}/actions/${encodeURIComponent(item.action)}/preview`,{cache:"no-store"});if(!response.ok)throw{status:response.status};preview=await response.json()}catch(error){hostNotice(storagePanel,"Storage preview unavailable",hostFailure(error.status,"storage"));return}
    if(!preview.eligibility?.allowed){hostNotice(storagePanel,"Storage operation denied",preview.eligibility?.reason||"The current safety policy denied this operation.");return}
    const dialog=document.createElement("dialog");dialog.className="service-action-dialog storage-action-dialog";const form=document.createElement("form");form.method="dialog";const title=document.createElement("h2");presentHostText(title,`${storageActionLabel(preview.action)} ${preview.resource_id}`);const details=document.createElement("dl");details.className="info-list";[["Source",preview.source],["Target",preview.target],["Filesystem",preview.filesystem_type],["Current state",preview.mounted?"Mounted":"Unmounted"],["Persistence",preview.persistence?`${preview.persistence.state} · ${preview.persistence.health}`:"Not configured"],["Expected result",preview.expected_result],["Risk",preview.eligibility?.risk||"controlled"]].forEach(([label,value])=>hostRow(details,label,value));const warning=document.createElement("p");warning.className="storage-note";presentHostText(warning,"KITPro will revalidate resource identity, mount relationships, target safety, authorization, and policy immediately before dispatch.");const confirm=document.createElement("label");confirm.className="field-label";presentHostText(confirm,`Type ${preview.resource_id} to confirm`);const typed=document.createElement("input");typed.required=true;typed.autocomplete="off";confirm.append(typed);const controls=document.createElement("div");controls.className="service-actions";const cancel=document.createElement("button");cancel.type="button";cancel.className="button";presentHostText(cancel,"Cancel");cancel.addEventListener("click",()=>dialog.close());const submit=document.createElement("button");submit.type="button";submit.className=`button button-primary${(preview.action==="unmount"||preview.action==="disable_persistence")?" button-danger":""}`;presentHostText(submit,`Confirm ${preview.action}`);submit.addEventListener("click",async()=>{if(typed.value!==preview.resource_id){typed.setCustomValidity(`Type ${preview.resource_id} exactly.`);typed.reportValidity();return}submit.disabled=true;presentHostText(submit,"Operation running…");await runStorageAction(preview,dialog)});controls.append(cancel,submit);form.append(title,details,warning,confirm,controls);dialog.append(form);document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal()
  }
  async function runStorageAction(preview,dialog){const csrf=document.querySelector('input[name="csrf_token"]')?.value||"";try{const response=await fetch(`/api/v1/host/storage/${encodeURIComponent(preview.resource_id)}/actions`,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify({action:preview.action,confirmation_resource:preview.resource_id})});const result=await response.json().catch(()=>({}));if(!response.ok)throw{status:response.status,result};dialog.close();hostNotice(storagePanel,result.status==="completed"?"Storage operation completed":"Storage operation requires review",result.id?`Operation ${result.id}: ${result.status}.`:result.summary);await loadStorage();loadHealth();loadDashboard()}catch(error){dialog.close();hostNotice(storagePanel,"Storage operation failed",friendlyError(error.status,error.result?.error,operationOutcome(error.status,error.result||{}),error.result?.id||""))}}
  async function loadStorage() {
    if (!storagePanel) return;
    hostNotice(storagePanel,"Loading storage","Reading block devices, mounted filesystems, and optional storage providers.");
    try {
      const response=await fetch("/api/v1/host/storage",{cache:"no-store"}); if(!response.ok) throw {status:response.status};
      const data=await response.json(); const summary=data.summary||{};
      const overview=document.createElement("div"); overview.className="summary-strip storage-summary";
      overview.append(storageMetric(hostBytes(summary.available_bytes),"Available local capacity"),storageMetric(hostBytes(summary.used_bytes),"Used local capacity"),storageMetric(summary.mounted_filesystem_count??0,"Mounted filesystems"),storageMetric(summary.device_count??0,"Block devices"));
      const warnings=hostCard("Storage status",[["Capacity scope",summary.capacity_scope],["Warnings",summary.warning_count?`${summary.warning_count} items need attention`:"No capacity or read-only warnings"],["LVM",data.capabilities?.lvm?.detail||data.capabilities?.lvm?.state],["ZFS",data.capabilities?.zfs?.detail||data.capabilities?.zfs?.state]]);
      const advancedRows=[]; for(const item of data.lvm_volumes||[]) advancedRows.push([`LVM ${item.group}/${item.name}`,`${hostBytes(item.size_bytes)} · ${item.kind||"volume"}`]); for(const item of data.zfs_pools||[]) advancedRows.push([`ZFS pool ${item.name}`,`${hostBytes(item.free_bytes)} free · ${item.health||"health unavailable"}`]); for(const item of data.zfs_datasets||[]) advancedRows.push([`ZFS dataset ${item.name}`,`${hostBytes(item.available_bytes)} available · ${item.mount_point||"not mounted"}`]);
      const advanced=hostCard("Advanced storage",advancedRows.length?advancedRows:[["Status","No LVM or ZFS inventory is available on this host."]]);
      storagePanel.replaceChildren(overview,warnings,storageManagementSection(data.management||[]),storageTableSection("Disks and devices","Filter by device, model, or type",data.devices||[],storageDeviceRow,["Device","Model","Type","Capacity","Partitions","Mounts","Status"],"storage-devices-table","No block device matches this filter."),storageTableSection("Filesystems and mounts","Filter by source, filesystem, or mount point",data.filesystems||[],storageFilesystemRow,["Source","Filesystem","Mount point","Total","Used","Available","Usage","Mode"],"storage-filesystems-table","No mounted filesystem matches this filter."),storageTableSection("Persistent mount configuration","Filter by source, target, filesystem, or option",data.persistent_mounts||[],storagePersistentRow,["Source","Target","Filesystem","Options"],"storage-persistent-table","No persistent mount entries are configured or readable."),advanced);
    } catch(error) { hostNotice(storagePanel,"Storage unavailable",hostFailure(error.status,"storage")); }
  }

  function networkInterfacesSection(items,resources){
    const section=document.createElement("section");section.className="section-block";const heading=document.createElement("div");heading.className="section-heading";const copy=document.createElement("div");const title=document.createElement("h2");presentHostText(title,"Interfaces");const description=document.createElement("p");description.className="panel-description";presentHostText(description,"Observed kernel state. Configuration is available only for exact interfaces in the root-owned policy.");copy.append(title,description);heading.append(copy);section.append(heading);const table=adminTable("network-interfaces-table","Network interfaces",["Interface","Type","State","Addresses","Backend","Receive","Transmit","Configuration"]);for(const item of items){const row=document.createElement("tr");row.dataset.networkInterface=item.name;const name=tableCell(item.name,"mono network-interface");name.title=`Index ${item.index}; MAC ${item.mac_address||"unavailable"}; driver ${item.driver||"unavailable"}`;const addresses=tableCell((item.addresses||[]).map(networkAddressLabel).filter(Boolean).join(", ")||"None","mono network-addresses");addresses.title=addresses.textContent;const rx=tableCell(networkRate(item.stats?.rx_bytes_per_second),"mono");rx.dataset.networkRx="";const tx=tableCell(networkRate(item.stats?.tx_bytes_per_second),"mono");tx.dataset.networkTx="";const action=document.createElement("td");action.className="table-actions";const resource=resources.find(candidate=>candidate.interface===item.name);if(resource){const button=document.createElement("button");button.type="button";button.className="button button-small";presentHostText(button,"Configure");button.disabled=resource.protected;button.title=resource.protected?"This interface is protected by network policy.":"Preview a transactional configuration";button.addEventListener("click",()=>showNetworkConfiguration(item,resource));action.append(button)}else{presentHostText(action,"Read only")};row.append(name,tableCell(`${item.type} · ${item.classification}`),tableCell(`${item.administrative_state} / ${item.operational_state}`),addresses,tableCell(item.backend),rx,tx,action);table.body.append(row)}section.append(table.wrapper);return section
  }
  function networkRoutesSection(items){const section=document.createElement("section");section.className="section-block";const heading=document.createElement("div");heading.className="section-heading";const title=document.createElement("h2");presentHostText(title,"Routes and gateways");heading.append(title);section.append(heading);const table=adminTable("network-routes-table","Network routes",["Destination","Gateway","Interface","Source","Metric","Table","Protocol","Class"]);for(const item of items){const row=document.createElement("tr");row.append(tableCell(item.destination,"mono"),tableCell(item.gateway||"Direct","mono"),tableCell(item.interface,"mono"),tableCell(item.source||"—","mono"),tableCell(item.metric==null?"Default":item.metric),tableCell(item.table),tableCell(item.protocol),tableCell(item.container?"Container":item.default?"Host default":"Host"));table.body.append(row)}section.append(table.wrapper);return section}
  function networkHistorySection(items){const section=document.createElement("section");section.className="section-block";const heading=document.createElement("div");heading.className="section-heading";const title=document.createElement("h2");presentHostText(title,"Configuration history");heading.append(title);section.append(heading);if(!items.length){const empty=document.createElement("p");empty.className="panel-description";presentHostText(empty,"No network configuration transactions have been recorded.");section.append(empty);return section}const table=adminTable("network-history-table","Network configuration history",["Timestamp","Interface","Action","Result","Administrator","Details"]);for(const item of items){const row=document.createElement("tr");const parsed=new Date(item.timestamp);const timestamp=Number.isNaN(parsed.valueOf())?item.timestamp:parsed.toLocaleString();row.append(tableCell(timestamp,"mono"),tableCell(item.interface,"mono"),tableCell(item.action),tableCell(item.state),tableCell(item.administrator),tableCell(item.failure_reason||item.rollback_result||"Completed"));table.body.append(row)}section.append(table.wrapper);return section}
  async function showNetworkConfiguration(item,resource){const dialog=document.createElement("dialog");dialog.className="service-action-dialog network-configuration-dialog";const title=document.createElement("h2");presentHostText(title,`Configure ${item.name}`);const form=document.createElement("form");form.method="dialog";const mode=document.createElement("select");for(const value of resource.allowed_modes||[]){const option=document.createElement("option");option.value=value;presentHostText(option,value==="dhcp"?"DHCP IPv4":"Static IPv4");mode.append(option)}const address=document.createElement("input");address.placeholder="192.0.2.20/24";const gateway=document.createElement("input");gateway.placeholder="192.0.2.1";const dns=document.createElement("input");dns.placeholder="192.0.2.53, 192.0.2.54";const domains=document.createElement("input");domains.placeholder="example.test";[["Mode",mode],["IPv4 address and prefix",address],["IPv4 gateway",gateway],["DNS servers",dns],["Search domains",domains]].forEach(([label,control])=>{const field=document.createElement("label");field.className="field-label";presentHostText(field,label);field.append(control);form.append(field)});const note=document.createElement("p");note.className="storage-note";presentHostText(note,"KITPro will arm an independent server-side rollback before applying this change. The interface must be confirmed before the displayed deadline.");const controls=document.createElement("div");controls.className="service-actions";const cancel=document.createElement("button");cancel.type="button";cancel.className="button";presentHostText(cancel,"Cancel");cancel.addEventListener("click",()=>dialog.close());const preview=document.createElement("button");preview.type="button";preview.className="button button-primary";presentHostText(preview,"Preview change");preview.addEventListener("click",async()=>{const candidate={mode:mode.value,addresses:mode.value==="static"&&address.value.trim()?[address.value.trim()]:[],gateway:mode.value==="static"?gateway.value.trim():"",dns_servers:dns.value.split(",").map(v=>v.trim()).filter(Boolean),search_domains:domains.value.split(",").map(v=>v.trim()).filter(Boolean)};await previewNetworkConfiguration(dialog,item,candidate)});controls.append(cancel,preview);form.append(note,controls);dialog.append(title,form);document.body.append(dialog);dialog.addEventListener("close",()=>dialog.remove());dialog.showModal()}
  async function previewNetworkConfiguration(dialog,item,candidate){const csrf=document.querySelector('input[name="csrf_token"]')?.value||"";try{const response=await fetch(`/api/v1/host/network/interfaces/${encodeURIComponent(item.name)}/configuration/preview`,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify({candidate})});const preview=await response.json();if(!response.ok||!preview.eligibility?.allowed)throw{status:response.status,result:preview};const title=document.createElement("h2");presentHostText(title,`Review ${item.name}`);const details=document.createElement("dl");details.className="info-list";[["Backend",preview.proposed.backend],["Mode",preview.proposed.candidate.mode],["Addresses",preview.proposed.candidate.addresses?.join(", ")||"DHCP assigned"],["Gateway",preview.proposed.candidate.gateway||"Backend assigned"],["DNS",preview.proposed.candidate.dns_servers?.join(", ")||"Backend assigned"],["Recovery",preview.eligibility.recovery_ready?"Independent rollback ready":"Unavailable"],["Confirmation window",`${preview.confirmation_deadline_seconds} seconds`]].forEach(([label,value])=>hostRow(details,label,value));const field=document.createElement("label");field.className="field-label";presentHostText(field,`Type ${item.name} to confirm the exact interface`);const typed=document.createElement("input");typed.autocomplete="off";field.append(typed);const controls=document.createElement("div");controls.className="service-actions";const cancel=document.createElement("button");cancel.type="button";cancel.className="button";presentHostText(cancel,"Cancel");cancel.addEventListener("click",()=>dialog.close());const applyButton=document.createElement("button");applyButton.type="button";applyButton.className="button button-primary";presentHostText(applyButton,"Apply with rollback");applyButton.addEventListener("click",async()=>{if(typed.value!==item.name){typed.setCustomValidity(`Type ${item.name} exactly.`);typed.reportValidity();return}applyButton.disabled=true;const apply=await fetch(`/api/v1/host/network/interfaces/${encodeURIComponent(item.name)}/configuration/apply`,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify({candidate,confirmation_interface:item.name,expected_digest:preview.current.digest})});const result=await apply.json();if(!apply.ok){dialog.close();hostNotice(networkPanel,"Network configuration failed",friendlyError(apply.status,result.error,operationOutcome(apply.status,result),result.id||""));return}dialog.close();await showNetworkConfirmation(item.name,result)});controls.append(cancel,applyButton);dialog.replaceChildren(title,details,field,controls)}catch(error){dialog.close();hostNotice(networkPanel,"Network configuration denied",error.result?.eligibility?.reason||friendlyError(error.status,error.result?.error,operationOutcome(error.status,error.result||{}),error.result?.id||""))}}
  async function showNetworkConfirmation(interfaceName,result){const dialog=document.createElement("dialog");dialog.className="service-action-dialog network-configuration-dialog";const title=document.createElement("h2");presentHostText(title,`Confirm ${interfaceName}`);const status=document.createElement("p");status.className="storage-note";const deadline=new Date(result.deadline);const update=()=>{const seconds=Math.max(0,Math.ceil((deadline-Date.now())/1000));presentHostText(status,`${seconds} seconds remain. Server-side rollback is armed independently of this page.`)};update();const timer=setInterval(update,1000);const controls=document.createElement("div");controls.className="service-actions";const rollback=document.createElement("button");rollback.type="button";rollback.className="button button-danger";presentHostText(rollback,"Roll back now");const confirm=document.createElement("button");confirm.type="button";confirm.className="button button-primary";presentHostText(confirm,"Confirm configuration");const finish=async(action)=>{const csrf=document.querySelector('input[name="csrf_token"]')?.value||"";const response=await fetch(`/api/v1/host/network/transactions/${encodeURIComponent(result.id)}/${action}`,{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify({candidate_digest:result.candidate_digest,confirmation_interface:interfaceName})});const data=await response.json().catch(()=>({}));clearInterval(timer);dialog.close();hostNotice(networkPanel,response.ok?"Network transaction completed":"Network transaction requires review",`Transaction ${result.id}: ${data.state||"outcome unknown"}.`);await loadNetwork()};rollback.addEventListener("click",()=>finish("rollback"));confirm.addEventListener("click",()=>finish("confirm"));controls.append(rollback,confirm);dialog.append(title,status,controls);document.body.append(dialog);dialog.addEventListener("close",()=>{clearInterval(timer);dialog.remove()});dialog.showModal()}
  function networkBackendRows(backends) {
    return Object.entries(backends||{}).map(([name,status])=>[name.replaceAll("_"," "),status.detail||status.state]);
  }
  async function refreshNetworkStats() {
    if (location.hash!=="#host-network"||document.hidden)return;
    try {
      const response=await fetch("/api/v1/host/network/stats",{cache:"no-store"});if(!response.ok)return;
      const data=await response.json();
      for(const item of data.items||[]){
        const row=networkPanel?.querySelector(`[data-network-interface="${CSS.escape(item.name)}"]`);if(!row)continue;
        presentHostText(row.querySelector("[data-network-rx]"),networkRate(item.stats?.rx_bytes_per_second));presentHostText(row.querySelector("[data-network-tx]"),networkRate(item.stats?.tx_bytes_per_second));
      }
    } catch (_) { /* The inventory remains visible when a sample is missed. */ }
  }
  function startNetworkPolling(){if(networkPollTimer)clearInterval(networkPollTimer);networkPollTimer=setInterval(refreshNetworkStats,5000);}
  function stopNetworkPolling(){if(networkPollTimer){clearInterval(networkPollTimer);networkPollTimer=null;}}
  async function loadNetwork() {
    if(!networkPanel)return;
    hostNotice(networkPanel,"Loading network","Reading interfaces, addresses, routes, DNS, and traffic counters.");
    try{
      const [response,capabilityResponse,historyResponse]=await Promise.all([fetch("/api/v1/host/network",{cache:"no-store"}),fetch("/api/v1/host/network/configuration/capabilities",{cache:"no-store"}),fetch("/api/v1/host/network/history?limit=20",{cache:"no-store"})]);if(!response.ok)throw{status:response.status};
      const data=await response.json();const capabilities=capabilityResponse.ok?await capabilityResponse.json():{resources:[]};const history=historyResponse.ok?await historyResponse.json():{items:[]};const summary=data.summary||{};
      const overview=document.createElement("div");overview.className="summary-strip network-summary";
      overview.append(storageMetric(summary.active_interface_count??0,"Active interfaces"),storageMetric(summary.default_route_count??0,"Host default routes"),storageMetric(summary.resolver_count??0,"Configured resolvers"),storageMetric(summary.health||"Unavailable","Observed network state"));
      const state=hostCard("Network status",[["Assessment",summary.health_detail],["Traffic scope",summary.traffic_scope],["Address inventory",data.capabilities?.addresses?.detail||data.capabilities?.addresses?.state],["Policy routing",data.capabilities?.policy_routing?.detail||data.capabilities?.policy_routing?.state],["Configuration changes",capabilities.enabled?"Transactional changes enabled for approved interfaces":"No approved mutable interfaces"]]);
      const backendRows=networkBackendRows(data.backends);
      const backends=hostCard("Network backends",backendRows.length?backendRows:[["Status","No backend information is available."]]);
      const interfaces=networkInterfacesSection(data.interfaces||[],capabilities.resources||[]);
      const routes=networkRoutesSection(data.routes||[]);
      const dns=data.dns||{};
      const perLink=(dns.interfaces||[]).map(item=>`${item.interface}: ${item.servers?.join(", ")||"domains only"} ${item.domains?.join(", ")||""}`).join("; ");
      const dnsCard=hostCard("DNS configuration",[["Servers",dns.servers?.join(", ")||"None configured"],["Search domains",dns.search_domains?.join(", ")||"None"],["Per-interface DNS",perLink||"Unavailable"],["Source",dns.source],["Resolver link",dns.resolv_conf_target||dns.resolv_conf_path],["Configuration status",dns.status?.detail||dns.status?.state],["DNSSEC",dns.dnssec||"Unavailable"],["Split DNS",dns.split_dns?.detail||dns.split_dns?.state]]);
      networkPanel.replaceChildren(overview,state,backends,interfaces,routes,dnsCard,networkHistorySection(history.items||[]));
      startNetworkPolling();
    }catch(error){stopNetworkPolling();hostNotice(networkPanel,"Network unavailable",hostFailure(error.status,"network"));}
  }
  async function runNetworkDiagnostic(event){
    event.preventDefault();if(!networkDiagnosticForm||!networkDiagnosticResult)return;
    hostNotice(networkDiagnosticResult,"Running diagnostic","The bounded check is in progress.");
    const form=new FormData(networkDiagnosticForm);const payload={operation:form.get("operation"),target:String(form.get("target")||"").trim()};
    const port=Number(form.get("port"));if(Number.isInteger(port)&&port>0)payload.port=port;
    const csrf=document.querySelector('input[name="csrf_token"]')?.value||"";
    try{
      const response=await fetch("/api/v1/host/network/diagnostics",{method:"POST",headers:{"Content-Type":"application/json","X-CSRF-Token":csrf},body:JSON.stringify(payload)});
      if(!response.ok)throw{status:response.status};
      const result=await response.json();
      networkDiagnosticResult.replaceChildren(hostCard("Diagnostic result",[["Operation",result.operation],["Target",result.target],["Result",result.success?"Succeeded":result.category],["Duration",`${result.duration_ms} ms`],["Detail",result.detail],["Addresses",result.addresses?.join(", ")],["Interface",result.interface],["Gateway",result.gateway],["Source",result.source],["Packet loss",result.packet_loss_percent==null?null:`${result.packet_loss_percent}%`],["Response time",result.response_time_ms==null?null:`${result.response_time_ms} ms`]]));
    }catch(error){hostNotice(networkDiagnosticResult,"Diagnostic unavailable",hostFailure(error.status,"network"));}
  }

  document.querySelector('[data-nav="host-health"]')?.addEventListener("click", loadHealth);
  document.querySelector("[data-health-refresh]")?.addEventListener("click", loadHealth);
  document.querySelector('[data-nav="dashboard"]')?.addEventListener("click", () => { loadDashboard(); startDashboardPolling(); });
  serviceForm?.addEventListener("submit", (event) => {event.preventDefault();loadServices(false);});
  logsForm?.addEventListener("submit", (event) => {event.preventDefault();loadHostLogs(false);});
  document.querySelector("[data-host-services-next]")?.addEventListener("click", () => loadServices(true));
  document.querySelector("[data-host-logs-next]")?.addEventListener("click", () => loadHostLogs(true));
  document.querySelector('[data-nav="host-services"]')?.addEventListener("click", () => loadServices(false));
  document.querySelector('[data-nav="host-logs"]')?.addEventListener("click", () => loadHostLogs(false));
  document.querySelector('[data-nav="host-storage"]')?.addEventListener("click", loadStorage);
  document.querySelector('[data-nav="host-network"]')?.addEventListener("click", loadNetwork);
  networkDiagnosticForm?.addEventListener("submit", runNetworkDiagnostic);
  if (location.hash === "#host-health") loadHealth();
  if (location.hash === "" || location.hash === "#dashboard") { loadDashboard(); startDashboardPolling(); }
  if (location.hash === "#host-services") loadServices(false);
  if (location.hash === "#host-logs") loadHostLogs(false);
  if (location.hash === "#host-storage") loadStorage();
  if (location.hash === "#host-network") loadNetwork();

  window.addEventListener("hashchange", () => { showView(); if(location.hash!=="#host-network")stopNetworkPolling(); if(location.hash!==""&&location.hash!=="#dashboard")stopDashboardPolling(); if(location.hash==="#host-health")loadHealth(); if(location.hash==="#host-storage")loadStorage(); if(location.hash==="#host-services")loadServices(false); if(location.hash==="#host-network")loadNetwork(); if(location.hash===""||location.hash==="#dashboard"){loadDashboard();startDashboardPolling();} });
  document.addEventListener("visibilitychange", () => { if(document.hidden){stopNetworkPolling();stopDashboardPolling();} else if(location.hash==="#host-network")startNetworkPolling(); else if(location.hash===""||location.hash==="#dashboard")startDashboardPolling(); });
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
