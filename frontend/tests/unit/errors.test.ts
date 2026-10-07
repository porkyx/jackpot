import { describe, expect, it } from "vitest";
import { BackendRejected, ProtocolError, TransportError } from "../../src/contracts/backend";
import * as Errors from "../../src/app/errors";

const cases: ReadonlyArray<readonly [Errors.AppError, Errors.ErrorAction, Errors.ErrorField | null, boolean]> = [
  [new Errors.ValidationError({ field: "prizes" }), "correct-input", "prizes", false],
  [new Errors.RevisionConflict(), "refresh", null, false],
  [new Errors.IncompleteSnapshot(), "block-draw", null, false],
  [new Errors.UnsupportedArticle(), "block-draw", "articleUrl", false],
  [new TransportError(), "refresh", null, true],
  [new Errors.ReadTimeout(), "refresh", null, true],
  [new Errors.MutationOutcomeUnknown(), "reconcile-operation", null, false],
  [new ProtocolError(), "stop-input", null, false],
  [new Errors.StorageError(), "retry-explicit", null, false],
  [new Errors.EntropyError(), "retry-explicit", null, false],
  [new Errors.ExportError(), "preserve-result", null, false],
  [new Errors.ClipboardError(), "preserve-result", null, false],
  [new Errors.Cancelled(), "none", null, false],
  [new Errors.UnexpectedDefect(), "diagnostic", null, false],
];

describe("frontend error policy", () => {
  for (const [error, action, field, retry] of cases) {
    it(`${error._tag} has a Korean message, explicit action and safe retry policy`, () => {
      const projected = Errors.projectError(error);
      expect(projected.code).toBe(error._tag);
      expect(projected.message).toMatch(/[가-힣]/);
      expect(projected.action).toBe(action);
      expect(projected.field).toBe(field);
      expect(projected.automaticReadRetry).toBe(retry);
      expect(projected.diagnosticId).toBe(error._tag === "UnexpectedDefect" ? "FE-UNEXPECTED" : null);
      expect(Object.isFrozen(projected)).toBe(true);
      expect(Object.keys(projected).sort()).toEqual(["action", "automaticReadRetry", "code", "diagnosticId", "field", "message"]);
    });

    it(`${error._tag} never copies secret details or stack into display or logs`, () => {
      const secret = "SECRET_PASSWORD_comment_identifier_C:\\Users\\private\\jackpot.sqlite3";
      Object.assign(error, { details: { password: secret, comment: secret, identifier: secret, path: secret }, stack: secret });
      const display = JSON.stringify(Errors.projectError(error));
      const log = JSON.stringify(Errors.safeErrorLog(error, "operation", 123));
      expect(Errors.projectError(error).message).not.toContain(secret);
      expect(display + log).not.toContain("SECRET_PASSWORD");
      expect(Object.keys(Errors.safeErrorLog(error, "operation", 123)).sort()).toEqual(["code", "occurredAtMillis", "stage"]);
    });
  }

  for (const field of ["form", "articleUrl", "selection", "prizes", "message", "scheduledAt"] as const) {
    it(`ValidationError connects only the allowed ${field} field`, () => {
      expect(Errors.projectError(new Errors.ValidationError({ field })).field).toBe(field);
    });
  }

  for (const code of ["InvalidInput", "InvalidState", "StaleRevision", "StaleArticleContext", "BackendSessionChanged", "StorageUnavailable"] as const) {
    it(`backend ${code} uses its known safe code without exposing messageKey`, () => {
      const error = new BackendRejected({ code, messageKey: "SECRET_PASSWORD C:\\private\\db.sqlite3" });
      const projected = Errors.projectError(error);
      expect(projected.code).toBe(code);
      expect(projected.message).toMatch(/[가-힣]/);
      const actions = { InvalidInput: "correct-input", InvalidState: "refresh", StaleRevision: "refresh", StaleArticleContext: "refresh", BackendSessionChanged: "stop-input", StorageUnavailable: "retry-explicit" } as const;
      expect(projected.action).toBe(actions[code]);
      expect(projected.field).toBe(code === "InvalidInput" ? "form" : null);
      expect(projected.automaticReadRetry).toBe(false);
      expect(projected.message).not.toContain(error.messageKey);
      expect(JSON.stringify(projected)).not.toContain("SECRET_PASSWORD");
      expect(JSON.stringify(Errors.safeErrorLog(error, "bootstrap", 0))).not.toContain("SECRET_PASSWORD");
    });
  }

  for (const code of ["ProtocolError", "", "Unknown", "TransportError", "SECRET_PASSWORD", "C:\\private\\db.sqlite3"]) {
    it(`backend code ${code || "empty"} becomes a protocol error without raw output or retry`, () => {
      const error = new BackendRejected({ code, messageKey: "SECRET_COMMENT" });
      expect(Errors.projectError(error)).toEqual(Errors.projectError(new ProtocolError()));
      expect(Errors.safeErrorLog(error, "pending-recovery", 0).code).toBe("ProtocolError");
    });
  }

  it("defect projection does not read, stringify or expose a hostile cause", () => {
    let inspected = 0;
    const cause = { get message() { inspected++; throw new Error("SECRET"); }, toJSON() { inspected++; throw new Error("SECRET"); }, toString() { inspected++; throw new Error("SECRET"); } };
    const projected = Errors.projectDefect(cause);
    expect(projected.code).toBe("UnexpectedDefect");
    expect(projected.diagnosticId).toBe("FE-UNEXPECTED");
    expect(projected.action).toBe("diagnostic");
    expect(inspected).toBe(0);
    expect(Errors.projectDefect(null)).toEqual(projected);
    expect(Errors.projectDefect(undefined)).toEqual(projected);
  });

  for (const stage of ["bootstrap", "pending-recovery", "operation", "render", "export", "clipboard"] as const) {
    it(`diagnostics accept the fixed ${stage} stage`, () => {
      const log = Errors.safeErrorLog(new TransportError(), stage, Number.MAX_SAFE_INTEGER);
      expect(log).toEqual({ code: "TransportError", stage, occurredAtMillis: Number.MAX_SAFE_INTEGER });
      expect(Object.isFrozen(log)).toBe(true);
    });
  }

  for (const timestamp of [NaN, Infinity, -Infinity, -1, 0.5, Number.MAX_SAFE_INTEGER + 1]) {
    it(`invalid diagnostic timestamp ${timestamp} fails loudly`, () => {
      expect(() => Errors.safeErrorLog(new TransportError(), "bootstrap", timestamp)).toThrow("Invalid diagnostic timestamp");
    });
  }

  it("zero timestamp is valid without invented time", () => {
    expect(Errors.safeErrorLog(new TransportError(), "bootstrap", 0).occurredAtMillis).toBe(0);
  });

  it("unknown runtime error tag is an invariant failure with no secret detail", () => {
    const error = new TransportError();
    Reflect.set(error, "_tag", "SECRET_UNKNOWN_TAG");
    expect(() => Errors.projectError(error)).toThrow("Unknown frontend error contract");
  });

  it("unknown runtime field is an invariant failure rather than unsafe DOM identity", () => {
    const error = new Errors.ValidationError({ field: "form" });
    Reflect.set(error, "field", "SECRET_FIELD");
    expect(() => Errors.projectError(error)).toThrow("Unknown frontend error contract");
  });

  it("unknown runtime log stage fails instead of copying untrusted text", () => {
    const stage: { value: Errors.ErrorStage } = { value: "bootstrap" };
    Reflect.set(stage, "value", "SECRET_STAGE");
    expect(() => Errors.safeErrorLog(new TransportError(), stage.value, 0)).toThrow("Unknown frontend error contract");
  });
});
