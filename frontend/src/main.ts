import { Effect, Layer, Stream } from "effect";
import { makeAppLive } from "./app/layers";
import { ResultImage, resultImageForDocument } from "./platform/canvas";
import { createAppRuntimeWithHmr } from "./app/runtime";
import { makeRouteManager } from "./app/routeManager";
import { initialShellState, projectShell, reduceShell } from "./app/shellState";
import { projectDefect, projectError, type ErrorProjection } from "./app/errors";
import { Backend, ProtocolError } from "./contracts/backend";
import { mountFoundationScreen, mountShell, shellEnvironment, type ShellView } from "./features/shell/view";
import { makeBootstrapSync, type BootstrapSyncSnapshot } from "./operations/bootstrapSync";
import { OperationCoordinator, type Received } from "./operations/coordinator";
import { makePendingRecovery, type PendingRecovery, type RecoverySnapshot } from "./operations/pendingRecovery";
import type { BootstrapReply } from "./contracts/backend";
import { DomPlatform } from "./platform/dom";
import { WailsBackendLive, WailsResultExportPorts, openWailsArticle } from "./platform/wails";
import { mountResultProduct } from "./features/results/view";
import { mountCreateScreenModel } from "./screens/create/screenOwner";
import type { DraftScreenContext } from "./screens/create/screenModel";
import { mountRecoveryStatus, type RecoveryStatusView } from "./ui/recovery/view";
import { makeCreateWorkspace } from "./screens/create/productOwner";
import { mountCreateProduct } from "./features/create/view";
import type { ProductCoordinator } from "./operations/productCoordinator";
import "./styles/base.css";

const environment = shellEnvironment();
let state = initialShellState(environment.systemAppearance());
let shell: ShellView | undefined;
let stopRequested = false;
let stop = () => { stopRequested = true; };
let dispatchRoute: (() => void) | undefined;
let dispatchRefresh: (() => void) | undefined;
let latestSnapshot: BootstrapSyncSnapshot | null = null;
let recoveredState: RecoverySnapshot | null = null;
let recoveryFailure: ErrorProjection | null = null;
let recoveryView: RecoveryStatusView | undefined;
let recoveryChecking = false;
let dispatchRecovery: (() => void) | undefined;
let product: ProductCoordinator | null = null;
let ensureProductDraft: (() => void) | undefined;
let setProductAvailability: ((blocked: boolean) => void) | undefined;
const pendingProduct: Array<Effect.Effect<void, never, DomPlatform>> = [];
let dispatchProduct = (effect: Effect.Effect<void, never, DomPlatform>): void => { pendingProduct.push(effect); };

function apply(snapshot: BootstrapSyncSnapshot): void {
  latestSnapshot = snapshot;
  const confirmed = snapshot.coordinator;
  const blocked = snapshot.phase !== "ready" || confirmed._tag !== "active" || confirmed.stale.length > 0 || confirmed.staleOverflow || recoveredState === null || recoveredState.backendSessionId !== confirmed.backendSessionId || recoveredState.writesBlocked;
  product?.setWritesBlocked(blocked);
  setProductAvailability?.(blocked);
  const failure = snapshot.failure ?? recoveryFailure ?? recoveredState?.failure ?? null;
  if (failure !== null) shell?.status(failure.message, true);
  else shell?.status(blocked ? "저장된 상태를 확인하고 있습니다. 변경 기능은 아직 사용할 수 없습니다." : "저장소 연결됨", false);
  if (!blocked) { state = reduceShell(state, { _tag: "BootReady" }); ensureProductDraft?.(); }
}
function draftContext(snapshot: BootstrapSyncSnapshot | null): DraftScreenContext | null {
  const draft = snapshot?.coordinator._tag === "active" ? snapshot.coordinator.activeDraft : null;
  return draft === null ? null : { backendSessionId: draft.backendSessionId, draftId: draft.draftId, revision: draft.revision, articleGeneration: draft.articleGeneration };
}

const initialize = Effect.gen(function*() {
  const mounted = yield* mountShell(environment, (theme) => {
    state = reduceShell(state, { _tag: "ThemeChanged", theme });
    const projection = projectShell(state); shell?.appearance(projection.appearance, state.theme);
  });
  shell = mounted.value;
  shell.appearance(projectShell(state).appearance, state.theme);
  recoveryView = yield* mountRecoveryStatus(shell.recoveryHost, () => dispatchRecovery?.());
  const dom = yield* DomPlatform;
  yield* dom.listen(environment.events, "pagehide", () => stop());
  yield* dom.listen(environment.events, "hashchange", () => dispatchRoute?.());
  yield* dom.listen(environment.events, "focus", () => dispatchRefresh?.());
  const resultImage = yield* ResultImage;
  const backend = yield* Backend; const coordinator = yield* OperationCoordinator; product = coordinator.product;
  let themeReceived = false;
  let latestBootstrap: Received<BootstrapReply> | undefined;
  const synchronized = yield* Effect.result(makeBootstrapSync({ bootstrap: () => coordinator.bootstrap().pipe(Effect.flatMap((received) => {
    const theme = received.reply.data.theme;
    if (theme !== "system" && theme !== "light" && theme !== "dark") return Effect.fail(new ProtocolError());
    if (!themeReceived) { themeReceived = true; state = reduceShell(state, { _tag: "ThemeChanged", theme }); shell?.appearance(projectShell(state).appearance, theme); shell?.restoredDraft(received.reply.data.activeDraft !== null); }
    latestBootstrap = received;
    return Effect.succeed(received.reply);
  })), subscribeStateChanges: backend.subscribeStateChanges }));
  const sync = synchronized._tag === "Success" ? synchronized.success : null;
  let recovery: PendingRecovery | null = null;
  let seededBootstrap = latestBootstrap;
  if (sync !== null && latestBootstrap !== undefined) {
    const recovered = yield* Effect.result(makePendingRecovery(coordinator, latestBootstrap));
    if (recovered._tag === "Success") {
      recovery = recovered.success; recoveredState = yield* recovery.snapshot;
      yield* recoveryView.patch(recoveredState, false);
      yield* recovery.resume.pipe(Effect.catch(() => Effect.void), Effect.forkScoped);
      yield* Stream.runForEach(recovery.changes, (snapshot) => Effect.gen(function*() { recoveredState = snapshot; if (latestSnapshot !== null) apply(latestSnapshot); if (recoveryView !== undefined) yield* recoveryView.patch(snapshot, recoveryChecking).pipe(Effect.catch(() => Effect.void)); })).pipe(Effect.forkScoped);
    } else recoveryFailure = recovered.failure._tag === "RecoveryUnavailable" ? projectDefect(undefined) : projectError(recovered.failure);
  }
  if (synchronized._tag === "Failure") {
    state = reduceShell(state, { _tag: "BootFailed", messageKey: synchronized.failure._tag === "ProtocolError" ? "ProtocolError" : synchronized.failure._tag === "BackendRejected" ? "StorageUnavailable" : "TransportError" });
    shell.status(projectError(synchronized.failure).message, true);
  }
  if (sync !== null) apply(yield* sync.snapshot);
  const workspace = product === null ? null : yield* makeCreateWorkspace(product, (effect) => dispatchProduct(effect));
  if (workspace !== null) {
    setProductAvailability = workspace.setWritesBlocked;
    if (latestSnapshot !== null) apply(latestSnapshot);
    let ensuring = false;
    ensureProductDraft = () => {
      if (ensuring || workspace.read().editor !== null || workspace.read().draft?.summary.state === "finalized") return;
      ensuring = true;
      dispatchProduct(workspace.ensure.pipe(Effect.catchCause(workspace.report), Effect.ensuring(Effect.sync(() => { ensuring = false; }))));
    };
    yield* Effect.addFinalizer(() => Effect.sync(() => { ensureProductDraft = undefined; setProductAvailability = undefined; }));
  }
  const routes = yield* makeRouteManager({
    mount: (lease) => Effect.gen(function*() {
      if (!lease.isCurrent()) return;
      state = reduceShell(state, { _tag: "Navigate", route: lease.route }); shell?.route(lease.route);
      if (product !== null && workspace !== null && lease.route._tag === "create") {
        yield* mountCreateProduct(mounted.value.outlet, product, workspace, (collectionId) => environment.navigateHash("#/results/" + encodeURIComponent(collectionId)));
      } else if (product !== null && lease.route._tag === "result") {
        yield* mountResultProduct(mounted.value.outlet, product, lease.route, (effect) => dispatchProduct(effect), { export: WailsResultExportPorts, openArticle: openWailsArticle, renderer: resultImage });
      } else yield* mountFoundationScreen(mounted.value.outlet, lease.route);
      if (lease.route._tag === "create" && product === null) {
        const screen = yield* mountCreateScreenModel(lease, draftContext(sync === null ? null : yield* sync.snapshot), null);
        if (sync !== null) yield* Stream.runForEach(sync.changes, (snapshot) => screen.updateContext(draftContext(snapshot)).pipe(Effect.catchTag("ScreenUnavailable", () => Effect.void))).pipe(Effect.forkScoped);
      }
    }),
    onFailure: () => Effect.sync(() => shell?.status(projectDefect(undefined).message, true)),
  });
  yield* routes.navigate(environment.readHash());
  if (sync !== null) {
    apply(yield* sync.snapshot);
    yield* Stream.runForEach(sync.changes, (snapshot) => Effect.gen(function*() {
      const received = latestBootstrap;
      if (recovery !== null && received !== undefined && received !== seededBootstrap && (snapshot.phase === "ready" || snapshot.phase === "stale") && snapshot.coordinator._tag === "active" && snapshot.coordinator.backendSessionId === received.reply.backendSessionId) {
        seededBootstrap = received;
        const accepted = yield* Effect.result(recovery.acceptBootstrap(received));
        if (accepted._tag === "Success") { recoveryFailure = null; recoveredState = accepted.success; yield* recovery.resume.pipe(Effect.catch(() => Effect.void), Effect.forkScoped); }
        else recoveryFailure = accepted.failure._tag === "RecoveryUnavailable" ? projectDefect(undefined) : projectError(accepted.failure);
      }
      apply(snapshot);
    })).pipe(Effect.forkScoped);
  }
  return { routes, sync, recovery };
});

const hot = import.meta.hot;
// Vite must treat the app entry as a boundary so each update awaits old ownership.
if (import.meta.hot) import.meta.hot.accept();
export const appOwner = await createAppRuntimeWithHmr(makeAppLive(WailsBackendLive, Layer.succeed(ResultImage, resultImageForDocument(environment.host.ownerDocument))), initialize, hot?.data === undefined ? undefined : hot, () => shell?.status(projectDefect(undefined).message, true));
stop = () => { void appOwner.dispose().catch(() => {}); };
if (stopRequested) stop();
try {
  const app = await appOwner.ready;
  dispatchProduct = (effect) => { void appOwner.run(effect).catch(() => shell?.status(projectDefect(undefined).message, true)); };
  for (const effect of pendingProduct.splice(0)) dispatchProduct(effect);
  dispatchRoute = () => { void appOwner.run(app.routes.navigate(environment.readHash())).catch(() => shell?.status(projectDefect(undefined).message, true)); };
  dispatchRecovery = () => {
    if (app.recovery === null || recoveryChecking) return;
    recoveryChecking = true;
    const recovery = app.recovery;
    void appOwner.run(Effect.gen(function*() {
      if (recoveryView !== undefined) yield* recoveryView.patch(yield* recovery.snapshot, true);
      yield* recovery.resync.pipe(Effect.catch(() => Effect.void));
    }).pipe(Effect.ensuring(Effect.gen(function*() {
      recoveryChecking = false;
      if (recoveryView !== undefined) yield* recoveryView.patch(yield* recovery.snapshot, false).pipe(Effect.catch(() => Effect.void));
    })))).catch(() => {});
  };
  dispatchRefresh = () => {
    if (app.sync !== null) void appOwner.run(app.sync.resync).catch(() => {});
    if (product !== null) void appOwner.run(product.refreshDraft.pipe(Effect.catch(() => Effect.void), Effect.andThen(product.recheck), Effect.catch(() => Effect.void))).catch(() => {});
  };
} catch { pendingProduct.length = 0; /* Typed startup failures were projected before Runtime cleanup. */ }
