import { Data } from "effect";
import { BackendRejected, ProtocolError, TransportError, type BackendError } from "../contracts/backend";

export type ErrorField = "form" | "articleUrl" | "selection" | "prizes" | "message" | "scheduledAt";
export class ValidationError extends Data.TaggedError("ValidationError")<{ readonly field: ErrorField }> {}
export class RevisionConflict extends Data.TaggedError("RevisionConflict")<{}> {}
export class IncompleteSnapshot extends Data.TaggedError("IncompleteSnapshot")<{}> {}
export class UnsupportedArticle extends Data.TaggedError("UnsupportedArticle")<{}> {}
export class ReadTimeout extends Data.TaggedError("ReadTimeout")<{}> {}
export class MutationOutcomeUnknown extends Data.TaggedError("MutationOutcomeUnknown")<{}> {}
export class StorageError extends Data.TaggedError("StorageError")<{}> {}
export class EntropyError extends Data.TaggedError("EntropyError")<{}> {}
export class ExportError extends Data.TaggedError("ExportError")<{}> {}
export class ClipboardError extends Data.TaggedError("ClipboardError")<{}> {}
// Construct only after the backend confirms cancellation. Fiber interruption
// itself is never converted to this successful-cancellation notice.
export class Cancelled extends Data.TaggedError("Cancelled")<{}> {}
export class UnexpectedDefect extends Data.TaggedError("UnexpectedDefect")<{}> {}

export type AppError = BackendError | ValidationError | RevisionConflict |
  IncompleteSnapshot | UnsupportedArticle | ReadTimeout | MutationOutcomeUnknown | StorageError | EntropyError |
  ExportError | ClipboardError | Cancelled | UnexpectedDefect;
export type ErrorAction = "correct-input" | "refresh" | "block-draw" | "reconcile-operation" |
  "stop-input" | "retry-explicit" | "preserve-result" | "none" | "diagnostic";
export type ErrorCode = Exclude<AppError["_tag"], "BackendRejected"> | "InvalidInput" | "InvalidState" |
  "StaleRevision" | "StaleArticleContext" | "BackendSessionChanged" | "StorageUnavailable";
export interface ErrorProjection {
  readonly code: ErrorCode;
  readonly message: string;
  readonly action: ErrorAction;
  readonly field: ErrorField | null;
  readonly automaticReadRetry: boolean;
  readonly diagnosticId: "FE-UNEXPECTED" | null;
}

function view(code: ErrorCode, message: string, action: ErrorAction, field: ErrorField | null = null): ErrorProjection {
  return Object.freeze({ code, message, action, field, automaticReadRetry: code === "TransportError" || code === "ReadTimeout", diagnosticId: code === "UnexpectedDefect" ? "FE-UNEXPECTED" : null });
}

function fieldFor(field: ErrorField): ErrorField {
  switch (field) {
    case "form": case "articleUrl": case "selection": case "prizes": case "message": case "scheduledAt": return field;
    default: return unreachable(field);
  }
}

function rejected(error: BackendRejected): ErrorProjection {
  // messageKey, message, details, stack and unknown codes are never copied.
  switch (error.code) {
    case "InvalidInput": return view("InvalidInput", "입력값을 확인해 주세요.", "correct-input", "form");
    case "InvalidState": return view("InvalidState", "현재 상태에서는 요청을 처리할 수 없습니다. 상태를 다시 확인해 주세요.", "refresh");
    case "StaleRevision": return view("StaleRevision", "상태가 변경되어 요청을 적용하지 않았습니다. 최신 상태를 확인해 주세요.", "refresh");
    case "StaleArticleContext": return view("StaleArticleContext", "게시글이 변경되어 이전 요청을 적용하지 않았습니다.", "refresh");
    case "BackendSessionChanged": return view("BackendSessionChanged", "앱 연결이 새로 시작되었습니다. 저장된 상태를 다시 확인해 주세요.", "stop-input");
    case "StorageUnavailable": return view("StorageUnavailable", "기록 저장소를 사용할 수 없습니다. 기존 기록과 상태를 확인해 주세요.", "retry-explicit");
    default: return projectError(new ProtocolError());
  }
}

export function projectError(error: AppError): ErrorProjection {
  switch (error._tag) {
    case "ValidationError": return view(error._tag, "입력값을 확인해 주세요.", "correct-input", fieldFor(error.field));
    case "RevisionConflict": return view(error._tag, "상태가 변경되어 요청을 적용하지 않았습니다. 최신 상태를 확인해 주세요.", "refresh");
    case "IncompleteSnapshot": return view(error._tag, "댓글 수집이 완료되지 않아 추첨을 진행할 수 없습니다.", "block-draw");
    case "UnsupportedArticle": return view(error._tag, "지원하지 않는 게시글입니다.", "block-draw", "articleUrl");
    case "TransportError": return view(error._tag, "앱 연결을 확인할 수 없습니다. 마지막 정상 화면에서 다시 조회해 주세요.", "refresh");
    case "ReadTimeout": return view(error._tag, "조회 응답이 늦어지고 있습니다. 마지막 정상 화면에서 다시 조회해 주세요.", "refresh");
    case "MutationOutcomeUnknown": return view(error._tag, "요청 결과를 아직 확인하지 못했습니다. 저장된 처리 상태를 확인하고 있습니다.", "reconcile-operation");
    case "ProtocolError": return view(error._tag, "앱 응답 형식을 확인할 수 없습니다. 해당 기능 입력을 중단하고 버전을 확인해 주세요.", "stop-input");
    case "StorageError": return view(error._tag, "기록 저장에 실패했습니다. 실제 저장 상태를 확인한 뒤 다시 시도해 주세요.", "retry-explicit");
    case "EntropyError": return view(error._tag, "추첨 난수 생성에 실패했습니다. 저장된 회차 상태를 확인해 주세요.", "retry-explicit");
    case "ExportError": return view(error._tag, "사진 저장에 실패했습니다. 추첨 결과는 유지됩니다.", "preserve-result");
    case "ClipboardError": return view(error._tag, "결과 텍스트 복사에 실패했습니다. 추첨 결과는 유지됩니다.", "preserve-result");
    case "Cancelled": return view(error._tag, "작업 취소가 확인되었습니다.", "none");
    case "UnexpectedDefect": return view(error._tag, "예상하지 못한 문제가 발생했습니다. 진단 코드 FE-UNEXPECTED를 확인해 주세요.", "diagnostic");
    case "BackendRejected": return rejected(error);
    default: return unreachable(error);
  }
}

// A defect can contain arbitrary user data. Never stringify or inspect it.
export function projectDefect(_defect: unknown): ErrorProjection {
  return projectError(new UnexpectedDefect());
}

export type ErrorStage = "bootstrap" | "pending-recovery" | "operation" | "render" | "export" | "clipboard";
export interface SafeErrorLog {
  readonly code: ErrorCode;
  readonly stage: ErrorStage;
  readonly occurredAtMillis: number;
}
export function safeErrorLog(error: AppError, stage: ErrorStage, occurredAtMillis: number): SafeErrorLog {
  if (!Number.isSafeInteger(occurredAtMillis) || occurredAtMillis < 0) throw new Error("Invalid diagnostic timestamp");
  switch (stage) {
    case "bootstrap": case "pending-recovery": case "operation": case "render": case "export": case "clipboard":
      return Object.freeze({ code: projectError(error).code, stage, occurredAtMillis });
    default: return unreachable(stage);
  }
}

function unreachable(_value: never): never { throw new Error("Unknown frontend error contract"); }
