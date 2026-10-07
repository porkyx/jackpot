import { expect, it } from "@effect/vitest";
import { BackendRejected, ProtocolError, TransportError } from "../../src/contracts/backend";
import { productErrorMessage } from "../../src/app/productErrors";
import { ProductUnavailable } from "../../src/operations/productCoordinator";

it("local product unavailable projects fixed public text independently of any view", () => {
  expect(productErrorMessage(new ProductUnavailable({ reason: "outcome_unknown" }))).toBe("현재 작업과 앱 연결 상태를 확인한 뒤 다시 시도해 주세요.");
});
it("backend failures preserve existing safe projection without copying private payload", () => {
  expect(productErrorMessage(new BackendRejected({ code: "InvalidInput", messageKey: "PRIVATE-message" }))).toBe("입력값을 확인해 주세요.");
  for (const failure of [new ProtocolError(), new TransportError(), new BackendRejected({ code: "PRIVATE-code", messageKey: "PRIVATE-message" })]) {
    const message = productErrorMessage(failure);
    expect(message.length).toBeGreaterThan(0);
    expect(message).not.toContain("PRIVATE");
  }
});
