import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const source = await readFile(new URL("./web.js", import.meta.url), "utf8");
const context = vm.createContext({ KITPRO_TEST_MODE: true });
vm.runInContext(source, context);

const { friendlyError, operationOutcome } = context.KITPRO_OPERATION_TEST_API;

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
