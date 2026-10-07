import { projectError } from "./errors";
import type { ProductError } from "../operations/productCoordinator";

export function productErrorMessage(error: ProductError): string {
  return error._tag === "ProductUnavailable" ? "현재 작업과 앱 연결 상태를 확인한 뒤 다시 시도해 주세요." : projectError(error).message;
}
