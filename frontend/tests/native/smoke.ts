// A separate verification entry. It is absent from the production index/build.
import { Call, Window } from "@wailsio/runtime";
import { Deferred, Effect, Layer } from "effect";
import { createAppRuntime } from "../../src/app/runtime";
import { Backend } from "../../src/contracts/backend";
import { OperationCoordinator, OperationCoordinatorLive } from "../../src/operations/coordinator";
import { ClientIdsLive } from "../../src/platform/ids";
import { WailsBackendLive } from "../../src/platform/wails";

const platform = Layer.merge(WailsBackendLive, ClientIdsLive);
const layer = OperationCoordinatorLive.pipe(Layer.provideMerge(platform));
const report = { bootstrapReads: 0, pendingReads: 0, operationReads: 0, notices: 0, session: "", disposed: false, mainMounted: false, mainScreens: 0, mainResynced: false, minimumWindow: false, keyboardVerified: false, mainDisposed: false, failure: "" };
const owner = createAppRuntime(layer, Effect.gen(function*() {
  const notice = yield* Deferred.make<void>();
  const backend = yield* Backend; const coordinator = yield* OperationCoordinator;
  yield* backend.subscribeStateChanges((event) => { report.notices++; if (event.entityId === "native-smoke-draft") Deferred.doneUnsafe(notice, Effect.void); }, () => { report.failure = "ProtocolError"; });
  const first = yield* coordinator.bootstrap(); report.bootstrapReads++; report.session = first.reply.backendSessionId;
  const pending = yield* coordinator.listPendingOperations({ cursor: null, limit: 64 }); report.pendingReads++;
  if (pending.reply.data.operations?.length !== 0) return yield* Effect.die("Unexpected pending data");
  const operation = yield* coordinator.getOperation("native-missing"); report.operationReads++;
  if (operation.reply.data.state !== "unknown" || operation.reply.data.collectionId !== null) return yield* Effect.die("Unexpected missing operation observation");
  yield* Effect.promise(() => Call.ByName("main.Probe.RequestNotice"));
  yield* Deferred.await(notice);
  const second = yield* coordinator.bootstrap(); report.bootstrapReads++;
  if (second.reply.backendSessionId !== report.session) return yield* Effect.die("Changed native session");
}));
try { await owner.ready; await owner.dispose(); report.disposed = owner.phase() === "stopped"; }
catch { report.failure = "Native verification failed"; await owner.dispose().catch(() => {}); }
if (report.failure === "") {
  const host = document.createElement("main"); host.id = "app"; document.body.append(host);
  const main = await import("../../src/main");
  try {
    const app = await main.appOwner.ready;
    report.mainMounted = host.querySelector("h1")?.textContent === "Jackpot" && host.querySelector('[role="status"]')?.textContent?.includes("저장소 연결됨") === true;
    if (!report.mainMounted) throw new Error("Main shell did not initialize");
    for (const [hash, title] of [["#/create", "일반 추첨"], ["#/history", "화면을 찾을 수 없습니다"], ["#/create", "일반 추첨"]] as const) {
      await main.appOwner.run(app.routes.navigate(hash));
      for (let turn = 0; turn < 16 && host.querySelector("h2")?.textContent !== title; turn++) await main.appOwner.run(Effect.yieldNow);
      if (host.querySelector("h2")?.textContent !== title) throw new Error("Native route differs");
      report.mainScreens++;
    }
    if (host.querySelector('nav a[href="#/history"]') !== null) throw new Error("Retired history navigation remains");
    if (app.sync === null) throw new Error("Main sync missing");
    const synchronized = await main.appOwner.run(app.sync.resync);
    report.mainResynced = synchronized.phase === "ready" && synchronized.coordinator._tag === "active" && synchronized.coordinator.backendSessionId === report.session;
    await Window.SetSize(1024, 720);
    const size = await Window.Size();
    report.minimumWindow = size.width === 1024 && size.height === 720 && host.querySelector("nav") !== null && host.querySelector("label select") !== null && document.documentElement.scrollWidth <= window.innerWidth;
    if (!report.mainResynced || !report.minimumWindow) throw new Error("Native main invariant failed");
    if (new URL(location.href).searchParams.get("keyboard") === "1") {
      const { runNativeKeyboardProbe } = await import("./keyboard.page");
      await runNativeKeyboardProbe(host); report.keyboardVerified = true;
    }
    await main.appOwner.dispose(); await main.appOwner.dispose();
    report.mainDisposed = main.appOwner.phase() === "stopped" && host.childElementCount === 0;
  } catch { report.failure = "Native main verification failed"; await main.appOwner.dispose().catch(() => {}); }
}
await Call.ByName("main.Probe.Report", report);
