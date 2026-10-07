export type Route =
  | { readonly _tag: "create" }
  | { readonly _tag: "result"; readonly collectionId: string; readonly roundId: string | null }
  | { readonly _tag: "not_found"; readonly hash: string };

export type ThemePreference = "system" | "light" | "dark";
export type Appearance = "light" | "dark";
export type ShellBoot = { readonly _tag: "starting" } | { readonly _tag: "ready" } | {
  readonly _tag: "failed"; readonly messageKey: "ProtocolError" | "TransportError" | "StorageUnavailable";
};
export type NoticeTarget =
  | { readonly _tag: "draft"; readonly draftId: string }
  | { readonly _tag: "collection"; readonly collectionId: string }
  | { readonly _tag: "round"; readonly collectionId: string; readonly roundId: string };
export interface AppNotice {
  readonly id: string;
  readonly operationId: string | null;
  readonly target: NoticeTarget;
  readonly read: boolean;
}
export interface ShellState {
  readonly boot: ShellBoot;
  readonly route: Route;
  readonly theme: ThemePreference;
  readonly systemAppearance: Appearance;
  readonly notices: ReadonlyArray<AppNotice>;
}
export type ShellAction =
  | { readonly _tag: "BootReady" }
  | { readonly _tag: "BootFailed"; readonly messageKey: Extract<ShellBoot, { readonly _tag: "failed" }>["messageKey"] }
  | { readonly _tag: "Navigate"; readonly route: Route }
  | { readonly _tag: "ThemeChanged"; readonly theme: ThemePreference }
  | { readonly _tag: "SystemAppearanceChanged"; readonly appearance: Appearance }
  | { readonly _tag: "NoticeAdded"; readonly notice: AppNotice }
  | { readonly _tag: "NoticeRead"; readonly noticeId: string }
  | { readonly _tag: "NoticeDismissed"; readonly noticeId: string };

export const MAX_APP_NOTICES = 128;
function unreachable(value: never): never { throw new Error(`Unknown shell action: ${String(value)}`); }
function ownRoute(route: Route): Route {
  switch (route._tag) {
    case "create": return Object.freeze({ _tag: "create" });
    case "result": return Object.freeze({ _tag: "result", collectionId: route.collectionId, roundId: route.roundId });
    case "not_found": return Object.freeze({ _tag: "not_found", hash: route.hash });
    default: return unreachable(route);
  }
}
function ownBoot(boot: ShellBoot): ShellBoot {
  switch (boot._tag) {
    case "starting": return Object.freeze({ _tag: "starting" });
    case "ready": return Object.freeze({ _tag: "ready" });
    case "failed": return Object.freeze({ _tag: "failed", messageKey: boot.messageKey });
    default: return unreachable(boot);
  }
}
function ownNotice(notice: AppNotice): AppNotice {
  if (notice.id.length === 0) throw new Error("Notice identity is required");
  const target = notice.target;
  let owned: NoticeTarget;
  switch (target._tag) {
    case "draft": owned = Object.freeze({ _tag: target._tag, draftId: target.draftId }); break;
    case "collection": owned = Object.freeze({ _tag: target._tag, collectionId: target.collectionId }); break;
    case "round": owned = Object.freeze({ _tag: target._tag, collectionId: target.collectionId, roundId: target.roundId }); break;
    default: return unreachable(target);
  }
  return Object.freeze({ id: notice.id, operationId: notice.operationId, target: owned, read: notice.read });
}
function ownShell(state: ShellState): ShellState {
  return Object.freeze({ boot: ownBoot(state.boot), route: ownRoute(state.route), theme: state.theme, systemAppearance: state.systemAppearance, notices: Object.freeze(state.notices.map(ownNotice)) });
}
export function initialShellState(systemAppearance: Appearance = "light"): ShellState {
  return ownShell({ boot: { _tag: "starting" }, route: { _tag: "create" }, theme: "system", systemAppearance, notices: [] });
}
export function reduceShell(state: ShellState, action: ShellAction): ShellState {
  switch (action._tag) {
    case "BootReady": return ownShell({ ...state, boot: { _tag: "ready" } });
    case "BootFailed": return ownShell({ ...state, boot: { _tag: "failed", messageKey: action.messageKey } });
    case "Navigate": return ownShell({ ...state, route: action.route });
    case "ThemeChanged": return ownShell({ ...state, theme: action.theme });
    case "SystemAppearanceChanged": return ownShell({ ...state, systemAppearance: action.appearance });
    case "NoticeAdded": {
      const notices = state.notices.filter((notice) => notice.id !== action.notice.id);
      return ownShell({ ...state, notices: [...notices, action.notice].slice(-MAX_APP_NOTICES) });
    }
    case "NoticeRead": return ownShell({ ...state, notices: state.notices.map((notice) => notice.id === action.noticeId ? { ...notice, read: true } : notice) });
    case "NoticeDismissed": return ownShell({ ...state, notices: state.notices.filter((notice) => notice.id !== action.noticeId) });
    default: return unreachable(action);
  }
}
export function projectShell(state: ShellState) {
  return Object.freeze({ boot: ownBoot(state.boot), route: ownRoute(state.route), appearance: state.theme === "system" ? state.systemAppearance : state.theme, unreadCount: state.notices.filter((notice) => !notice.read).length });
}
