import { Effect, type Scope } from "effect";
import type { Appearance, Route, ThemePreference } from "../../app/shellState";
import { DomPlatform } from "../../platform/dom";
import { mountView, patchText } from "../../ui/view";

export interface ShellEnvironment {
  readonly host: HTMLElement;
  readonly events: EventTarget;
  readonly readHash: () => string;
  readonly navigateHash: (hash: string) => void;
  readonly systemAppearance: () => Appearance;
}
export function shellEnvironment(): ShellEnvironment {
  const host = document.getElementById("app");
  if (host === null) throw new Error("앱 표시 영역이 없습니다.");
  return { host, events: window, readHash: () => window.location.hash, navigateHash: (hash) => { window.location.hash = hash; },
    systemAppearance: () => window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light" };
}
export interface ShellView {
  readonly outlet: HTMLElement;
  readonly recoveryHost: HTMLElement;
  readonly restoredDraft: (restored: boolean) => void;
  readonly status: (message: string, failed: boolean) => void;
  readonly appearance: (appearance: Appearance, preference: ThemePreference) => void;
  readonly route: (route: Route) => void;
}

export function mountShell(environment: ShellEnvironment, changeTheme: (theme: ThemePreference) => void) {
  return mountView(environment.host, Effect.gen(function*() {
    const dom = yield* DomPlatform;
    const doc = environment.host.ownerDocument;
    const element = doc.createElement("div"); element.className = "shell";
    const header = doc.createElement("header"); header.className = "shell-header";
    const brand = doc.createElement("div"); brand.className = "shell-brand";
    const mark = doc.createElement("span"); mark.className = "shell-mark"; mark.textContent = "J"; mark.setAttribute("aria-hidden", "true");
    const heading = doc.createElement("h1"); heading.textContent = "Jackpot";
    brand.append(mark, heading);
    const nav = doc.createElement("nav"); nav.setAttribute("aria-label", "주 메뉴");
    const create = doc.createElement("a"); create.href = "#/create"; create.textContent = "만들기";
    nav.append(create);
    const label = doc.createElement("label"); label.className = "shell-theme"; label.textContent = "화면 색상 ";
    const theme = doc.createElement("select"); theme.name = "theme";
    for (const [value, text] of [["system", "시스템 설정"], ["light", "밝게"], ["dark", "어둡게"]] as const) {
      const option = doc.createElement("option"); option.value = value; option.textContent = text; theme.append(option);
    }
    label.append(theme); header.append(brand, nav, label);
    const status = doc.createElement("p"); status.className = "shell-status"; status.setAttribute("role", "status"); status.textContent = "앱 연결 확인 중";
    const error = doc.createElement("p"); error.className = "shell-error"; error.setAttribute("role", "alert"); error.hidden = true;
    const outlet = doc.createElement("section"); outlet.className = "screen"; outlet.setAttribute("aria-label", "현재 화면");
    const recoveryHost = doc.createElement("div");
    const restoredDraft = doc.createElement("p"); restoredDraft.setAttribute("role", "note"); restoredDraft.dataset.draftRecovery = "true"; restoredDraft.hidden = true;
    element.append(header, status, error, recoveryHost, restoredDraft, outlet);
    yield* dom.listen(theme, "change", () => {
      const value = theme.value;
      if (value === "system" || value === "light" || value === "dark") changeTheme(value);
    });
    const view: ShellView = {
      outlet, recoveryHost,
      restoredDraft: (restored) => { if (restored) patchText(restoredDraft, "현재 초안을 다시 불러왔습니다. 이전 화면에서 전송하지 않은 입력은 초기화되었습니다."); restoredDraft.hidden = !restored; },
      status: (message, failed) => { patchText(failed ? error : status, message); error.hidden = !failed; status.hidden = failed; },
      appearance: (appearance, preference) => { element.dataset.appearance = appearance; if (theme.value !== preference) theme.value = preference; },
      route: (route) => {
        if (route._tag === "create") create.setAttribute("aria-current", "page"); else create.removeAttribute("aria-current");
      },
    };
    return { element, value: view };
  }));
}

export function mountFoundationScreen(host: HTMLElement, route: Route): Effect.Effect<void, never, Scope.Scope> {
  return Effect.gen(function*() {
    const doc = host.ownerDocument;
    const element = doc.createElement("div");
    const heading = doc.createElement("h2");
    heading.textContent = route._tag === "create" ? "일반 추첨" : route._tag === "result" ? "추첨 결과" : "화면을 찾을 수 없습니다";
    const message = doc.createElement("p");
    message.textContent = route._tag === "not_found" ? "주 메뉴에서 화면을 선택해 주세요." : "이 화면의 추첨 기능을 준비하고 있습니다.";
    element.append(heading, message);
    yield* Effect.acquireRelease(Effect.sync(() => host.append(element)), () => Effect.sync(() => element.remove()));
  });
}
