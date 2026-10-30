import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("./web.js", import.meta.url), "utf8");
const stylesheet = await readFile(new URL("./web.css", import.meta.url), "utf8");
const markup = await readFile(new URL("./web.html", import.meta.url), "utf8");
const context = vm.createContext({ KITPRO_TEST_MODE: true });
vm.runInContext(source, context);

const { friendlyError, operationOutcome, presentCredential, presentHostText, storageWarningTone, networkRate, networkAddressLabel, healthLabel, healthTone, usedPercent, serviceActionLabel, serviceActionEffect } = context.KITPRO_OPERATION_TEST_API;

test("credential reveal populates text content without HTML persistence", () => {
  const target = { textContent: "••••••••••••" };
  const value = "a".repeat(64);
  presentCredential(target, value);
  assert.equal(target.textContent, value);
});

test("confirmed authentication rejection is a pre-submission failure", () => {
  assert.equal(operationOutcome(401, {}), "not-submitted");
  assert.match(friendlyError(401, "", "not-submitted"), /did not submit/);
});

test("a response with an operation ID identifies an existing operation", () => {
  assert.equal(operationOutcome(202, { id: "op-existing", status: "accepted" }), "existing-operation");
});

test("backend rejection after operation creation remains an existing operation", () => {
  assert.equal(operationOutcome(503, { id: "op-rejected", status: "failed" }), "existing-operation");
  assert.match(friendlyError(503, "helper rejected operation", "existing-operation", "op-rejected"), /op-rejected exists/);
});

test("503 with known operation identity does not become a retry instruction", () => {
  const outcome = operationOutcome(503, { id: "op-known" });
  assert.equal(outcome, "existing-operation");
  assert.doesNotMatch(friendlyError(503, "service unavailable", outcome, "op-known"), /try again/i);
});

test("network failure has an unknown outcome", () => {
  assert.equal(operationOutcome(0, {}, true), "unknown");
  assert.match(friendlyError(0, "network error", "unknown"), /could not confirm/);
});

test("failed and action-required results identify an existing operation", () => {
  for (const status of ["failed", "action_required"]) {
    assert.equal(operationOutcome(202, { status }), "existing-operation");
  }
});

test("operation error messages never recommend a blind safe retry", () => {
  for (const message of [
    friendlyError(401, "", "not-submitted"),
    friendlyError(503, "", "existing-operation", "op-known"),
    friendlyError(503, "", "unknown"),
  ]) {
    assert.doesNotMatch(message, /safe(?:ly)? (?:to )?retry/i);
  }
});

test("host log text is inserted as text", () => {
  const target = { textContent: "" };
  const malicious = "<img src=x onerror=alert(1)> token=private";
  presentHostText(target, malicious);
  assert.equal(target.textContent, malicious);
});

test("storage strings are inserted as text and warnings are classified", () => {
  const target = { textContent: "" };
  const malicious = "<svg onload=alert(1)> /dev/test";
  presentHostText(target, malicious);
  assert.equal(target.textContent, malicious);
  assert.equal(storageWarningTone(["capacity_warning"]), "warning");
  assert.equal(storageWarningTone(["capacity_critical"]), "danger");
  assert.equal(storageWarningTone(["mounted_read_only"]), "danger");
  assert.equal(storageWarningTone([]), "normal");
});


test("storage inventory uses compact semantic tables and mobile lists", () => {
  assert.match(source, /storageTableSection\("Disks and devices"[\s\S]*"storage-devices-table"/);
  assert.match(source, /adminTable\("storage-management-table","Approved storage operations"/);
  assert.match(source, /storageTableSection\("Filesystems and mounts"/);
  assert.match(source, /storageTableSection\("Persistent mount configuration"/);
  assert.doesNotMatch(source, /function storageDeviceCard\(/);
  assert.doesNotMatch(source, /function storageFilesystemCard\(/);
  assert.match(stylesheet, /\.storage-devices-table \{ min-width: 860px; \}/);
  assert.match(stylesheet, /@media \(max-width: 640px\)[\s\S]*\.storage-devices-table tr, \.storage-filesystems-table tr, \.storage-management-table tr \{ display: grid/);
  assert.match(stylesheet, /\.storage-device, \.storage-source, \.storage-mount, \.storage-model, \.storage-mounts \{[^}]*text-overflow: ellipsis;/);
});

test("storage mutations require a fresh preview and exact confirmation", () => {
  assert.match(source, /host\/storage\/\$\{encodeURIComponent\(item\.resource_id\)\}\/actions\/\$\{encodeURIComponent\(item\.action\)\}\/preview/);
  assert.match(source, /typed\.value!==preview\.resource_id/);
  assert.match(source, /confirmation_resource:preview\.resource_id/);
  assert.match(source, /KITPro will revalidate resource identity/);
  assert.doesNotMatch(source, /innerHTML/);
});

test("network values are formatted without interpreting host text", () => {
  const target = { textContent: "" };
  const malicious = "<img src=x onerror=alert(1)> veth";
  presentHostText(target, malicious);
  assert.equal(target.textContent, malicious);
  assert.equal(networkAddressLabel({address:"2001:db8::1", prefix_length:64}), "2001:db8::1/64");
  assert.equal(networkRate(1536), "1.5 KiB/s");
  assert.equal(networkRate(undefined), "Collecting…");
});

test("network inventory uses compact tables and transactional controls", () => {
  assert.match(source, /adminTable\("network-interfaces-table","Network interfaces"/);
  assert.match(source, /adminTable\("network-routes-table","Network routes"/);
  assert.doesNotMatch(source, /function networkInterfaceCard\(/);
  assert.doesNotMatch(source, /function networkRouteCard\(/);
  assert.match(source, /configuration\/preview/);
  assert.match(source, /configuration\/apply/);
  assert.match(source, /confirmation_interface:item\.name/);
  assert.match(source, /Independent rollback ready/);
  assert.match(stylesheet, /\.network-interfaces-table \{ min-width: 920px; \}/);
  assert.match(stylesheet, /\.network-routes-table \{ min-width: 880px; \}/);
  assert.match(stylesheet, /\[data-host-network\] \.settings-panel h2 \{ overflow-wrap: anywhere; \}/);
  assert.match(stylesheet, /@media \(max-width: 760px\)[\s\S]*\.network-diagnostic-form \{ grid-template-columns: minmax\(0, 1fr\); \}/);
});


test("health states remain distinct", () => {
  assert.equal(healthLabel("healthy"), "Healthy");
  assert.equal(healthLabel("not_configured"), "Not configured");
  assert.equal(healthLabel("unsupported"), "Unsupported");
  assert.equal(healthTone("critical"), "danger");
  assert.equal(healthTone("degraded"), "warning");
});

test("dashboard resource percentages validate inputs", () => {
  assert.equal(usedPercent(25, 100), 75);
  assert.equal(usedPercent(101, 100), null);
  assert.equal(usedPercent(undefined, 100), null);
});

test("health and dashboard layouts collapse on mobile", () => {
  assert.match(stylesheet, /\.health-component-grid, \.dashboard-grid \{ display: grid;/);
  assert.match(stylesheet, /@media \(max-width: 760px\)[\s\S]*\.health-component-grid, \.dashboard-grid \{ grid-template-columns: minmax\(0,1fr\); \}/);
  assert.match(stylesheet, /\.health-finding h3 \{[^}]*overflow-wrap: anywhere;/);
});

test("service actions describe exact effects and remain responsive", () => {
  assert.equal(serviceActionLabel("restart"), "Restart");
  assert.match(serviceActionEffect("stop"), /interrupt/);
  assert.match(source, /confirmation_unit:name/);
  assert.match(source, /typed\.value!==item\.name/);
  assert.match(stylesheet, /\.service-action-dialog \{ width: min\(520px, calc\(100vw - 32px\)\)/);
});

test("service catalog previews policy before dispatch", () => {
  assert.match(source, /actions\/\$\{encodeURIComponent\(action\)\}\/preview/);
  assert.match(source, /preview\.eligibility\?\.dependency_concerns/);
  assert.match(source, /KITPro will recheck policy immediately before dispatch/);
  assert.match(source, /Service activity/);
  assert.match(source, /services\/\$\{encodeURIComponent\(name\)\}\/history/);
  assert.match(source, /adminTable\("service-history-table","Service operation history"/);
  assert.doesNotMatch(source, /hostCard\(`\$\{serviceActionLabel\(item\.action\)\}/);
  assert.match(stylesheet, /\.service-history-table th:nth-child/);
  assert.match(stylesheet, /\.service-history-table tr \{ display: grid/);
  assert.match(source, /const request = \+\+serviceRequest/);
  assert.match(source, /if \(request !== serviceRequest\) return/);
  assert.match(source, /if \(request === serviceRequest\) hostNotice/);
});

test("services use a semantic dense table instead of listing cards", () => {
  assert.match(markup, /class="admin-table-region" data-host-services-results/);
  assert.match(source, /adminTable\("services-table", "System services", \["Service", "Description", "Status", "Startup", "PID", "Management", "Actions"\]\)/);
  assert.match(source, /body\.append\(serviceRow\(item\)\)/);
  assert.doesNotMatch(source, /function serviceCard\(/);
  assert.match(stylesheet, /\.services-table \{ min-width: 900px; \}/);
  assert.match(stylesheet, /\.service-name, \.service-description, \.service-startup, \.service-pid \{[^}]*text-overflow: ellipsis;/);
  assert.match(stylesheet, /@media \(max-width: 640px\)[\s\S]*\.services-table tr \{ display: grid;/);
  assert.match(source, /row-action-select/);
});

test("logs use a semantic dense table with bounded message rows", () => {
  assert.match(markup, /class="admin-table-region" data-host-logs-results/);
  assert.match(source, /adminTable\("logs-table", "System journal entries", \["Timestamp", "Severity", "Unit \/ source", "Message"\]\)/);
  assert.match(source, /body\.append\(logRow\(entry\)\)/);
  assert.doesNotMatch(source, /function logCard\(/);
  assert.match(stylesheet, /\.log-message > summary \{[^}]*-webkit-line-clamp: 2;/);
  assert.match(stylesheet, /@media \(max-width: 640px\)[\s\S]*\.logs-table tr \{ display: grid;/);
  assert.match(source, /presentHostText\(summary, entry\.message/);
});

test("services and logs preserve filtering and bounded pagination", () => {
  assert.match(source, /limit:"25", offset:String/);
  assert.match(source, /servicePager\.hidden = serviceOffset === null/);
  assert.match(source, /limit:"50"/);
  assert.match(source, /if \(more && logCursor\) params\.set\("cursor",logCursor\)/);
  assert.match(source, /logsPager\.hidden = !logCursor/);
});

test("storage persistence remains compact, typed, and confirmation-gated", () => {
  assert.match(source, /enable_persistence:"Enable persistence"/);
  assert.match(source, /disable_persistence:"Disable persistence"/);
  assert.match(source, /recover_persistence:"Recover configuration"/);
  assert.match(source, /\["Resource","Source","Target","Filesystem","State","Persistence","Policy","Action"\]/);
  assert.match(source, /item\.persistence\.state.*item\.persistence\.health/);
  assert.match(source, /confirmation_resource:preview\.resource_id/);
  assert.match(stylesheet, /\.storage-management-table \{ min-width: 940px; \}/);
  assert.match(stylesheet, /\.storage-management-table td:nth-child\(8\) \{ grid-column: 1 \/ -1; \}/);
  assert.doesNotMatch(source, /fstab.*POST/i);
});

test("network configuration history is a compact authenticated dataset", () => {
  assert.match(source, /fetch\("\/api\/v1\/host\/network\/history\?limit=20"/);
  assert.match(source, /adminTable\("network-history-table","Network configuration history",\["Timestamp","Interface","Action","Result","Administrator","Details"\]\)/);
  assert.match(source, /networkHistorySection\(history\.items\|\|\[\]\)/);
  assert.match(source, /const parsed=new Date\(item\.timestamp\);const timestamp=Number\.isNaN\(parsed\.valueOf\(\)\)\?item\.timestamp:parsed\.toLocaleString\(\)/);
  assert.match(stylesheet, /\.network-history-table \{ min-width: 760px; \}/);
  assert.match(stylesheet, /@media \(max-width: 640px\)[\s\S]*\.network-history-table tr \{ display: grid;/);
});
