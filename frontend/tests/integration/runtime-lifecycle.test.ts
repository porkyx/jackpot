import { expect, test } from "vitest";
import { Context, Deferred, Effect, Layer } from "effect";
import { createAppRuntime, createAppRuntimeWithHmr, RuntimeUnavailable, type RuntimeHotContext, type RuntimeHotData } from "../../src/app/runtime";

class Resource extends Context.Service<Resource, { readonly identity: number }>()("test/Resource") {}

function resources() {
  const state = { active: 0, opened: 0, closed: 0 };
  const layer = Layer.effect(Resource, Effect.acquireRelease(
    Effect.sync(() => { state.active++; state.opened++; return { identity: state.opened }; }),
    () => Effect.sync(() => { state.active--; state.closed++; }),
  ));
  return { state, layer };
}

test("one Runtime builds its Layer once and shares its services through all entrypoints", async () => {
  const { state, layer } = resources();
  const owner = createAppRuntime(layer, Resource);
  try {
    const initial = await owner.ready;
    expect(owner.phase()).toBe("running");
    expect(state).toEqual({ active: 1, opened: 1, closed: 0 });
    for (let index = 0; index < 10; index++) {
      expect(await owner.run(Resource)).toBe(initial);
      expect(await owner.mount(Resource)).toBe(initial);
    }
    expect(state.opened).toBe(1);
  } finally {
    await owner.dispose();
  }
  expect(owner.phase()).toBe("stopped");
  expect(state).toEqual({ active: 0, opened: 1, closed: 1 });
});

test("initialization and screen finalizers survive startup and close in screen-app-Layer order", async () => {
  const order: string[] = [];
  const layer = Layer.effect(Resource, Effect.acquireRelease(Effect.succeed({ identity: 1 }), () => Effect.sync(() => { order.push("layer"); })));
  const owner = createAppRuntime(layer, Effect.acquireRelease(Effect.succeed("ready"), () => Effect.sync(() => { order.push("app"); })));
  await owner.ready;
  await owner.mount(Effect.acquireRelease(Effect.void, () => Effect.sync(() => { order.push("screen"); })));
  expect(order).toEqual([]);
  const disposal = owner.dispose();
  expect(owner.phase()).toBe("stopping");
  expect(owner.dispose()).toBe(disposal);
  await disposal;
  expect(order).toEqual(["screen", "app", "layer"]);
  expect(owner.dispose()).toBe(disposal);
});

test("starting Runtime rejects dispatch without executing it", async () => {
  const { state, layer } = resources();
  const blocked = Deferred.makeUnsafe<void>();
  const owner = createAppRuntime(layer, Deferred.await(blocked));
  const readyFailure = owner.ready.catch((error: unknown) => error);
  let dispatched = 0;
  try {
    await expect(owner.run(Effect.sync(() => { dispatched++; }))).rejects.toEqual(new RuntimeUnavailable({ phase: "starting" }));
    await expect(owner.mount(Effect.sync(() => { dispatched++; }))).rejects.toEqual(new RuntimeUnavailable({ phase: "starting" }));
    expect(dispatched).toBe(0);
  } finally {
    await owner.dispose();
  }
  expect(await readyFailure).toBeDefined();
  expect(state.active).toBe(0);
  expect(state.opened).toBe(state.closed);
});

test("shutdown rejects new entrypoints immediately while asynchronous cleanup is pending", async () => {
  const { state, layer } = resources();
  const closing = Deferred.makeUnsafe<void>();
  const permitClose = Deferred.makeUnsafe<void>();
  const owner = createAppRuntime(layer, Effect.acquireRelease(Effect.void, () => Effect.gen(function*() {
    yield* Deferred.succeed(closing, undefined);
    yield* Deferred.await(permitClose);
  })));
  await owner.ready;
  const disposal = owner.dispose();
  await Effect.runPromise(Deferred.await(closing));
  let dispatched = 0;
  try {
    expect(owner.phase()).toBe("stopping");
    await expect(owner.run(Effect.sync(() => { dispatched++; }))).rejects.toEqual(new RuntimeUnavailable({ phase: "stopping" }));
    await expect(owner.mount(Effect.sync(() => { dispatched++; }))).rejects.toEqual(new RuntimeUnavailable({ phase: "stopping" }));
    expect(owner.dispose()).toBe(disposal);
    expect(dispatched).toBe(0);
  } finally {
    Deferred.doneUnsafe(permitClose, Effect.void);
    await disposal;
  }
  await expect(owner.run(Effect.void)).rejects.toEqual(new RuntimeUnavailable({ phase: "stopped" }));
  await expect(owner.mount(Effect.void)).rejects.toEqual(new RuntimeUnavailable({ phase: "stopped" }));
  expect(state.active).toBe(0);
});

test("initialization failure releases both partial app resources and Layer resources", async () => {
  const { state, layer } = resources();
  let partial = 0;
  const failure = new Error("initialization failed");
  const owner = createAppRuntime(layer, Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { partial++; }), () => Effect.sync(() => { partial--; }));
    return yield* Effect.fail(failure);
  }));
  await expect(owner.ready).rejects.toBe(failure);
  expect(owner.phase()).toBe("stopped");
  expect(partial).toBe(0);
  expect(state).toEqual({ active: 0, opened: 1, closed: 1 });
  await owner.dispose();
  expect(state.closed).toBe(1);
});

test("Layer acquisition failure aborts startup without invoking application initialization", async () => {
  const failure = new Error("Layer failed");
  let initialized = 0;
  const layer = Layer.effect(Resource, Effect.fail(failure));
  const owner = createAppRuntime(layer, Effect.sync(() => { initialized++; }));
  await expect(owner.ready).rejects.toBe(failure);
  expect(owner.phase()).toBe("stopped");
  expect(initialized).toBe(0);
  await owner.dispose();
});

test("canceling partial initialization interrupts its fiber and returns all resources to baseline", async () => {
  const { state, layer } = resources();
  const entered = Deferred.makeUnsafe<void>();
  let partial = 0;
  const order: string[] = [];
  const owner = createAppRuntime(layer, Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { partial++; }), () => Effect.sync(() => { partial--; order.push("app-resource"); }));
    yield* Deferred.succeed(entered, undefined);
    return yield* Effect.never.pipe(Effect.ensuring(Effect.sync(() => { order.push("initialization-fiber"); })));
  }));
  const ready = owner.ready.catch((error: unknown) => error);
  await Effect.runPromise(Deferred.await(entered));
  expect(partial).toBe(1);
  await owner.dispose();
  expect(await ready).toBeDefined();
  expect(partial).toBe(0);
  expect(order).toEqual(["initialization-fiber", "app-resource"]);
  expect(state).toEqual({ active: 0, opened: 1, closed: 1 });
});

test("shutdown interrupts pending app and screen fibers before releasing the Layer", async () => {
  const order: string[] = [];
  const layer = Layer.effect(Resource, Effect.acquireRelease(Effect.succeed({ identity: 1 }), () => Effect.sync(() => { order.push("layer"); })));
  const owner = createAppRuntime(layer, Effect.acquireRelease(Effect.void, () => Effect.sync(() => { order.push("app-resource"); })));
  await owner.ready;
  const appStarted = Deferred.makeUnsafe<void>();
  const screenStarted = Deferred.makeUnsafe<void>();
  const pending = (started: Deferred.Deferred<void>, name: string) => Effect.gen(function*() {
    yield* Deferred.succeed(started, undefined);
    return yield* Effect.never.pipe(Effect.ensuring(Effect.sync(() => { order.push(name); })));
  });
  const app = owner.run(pending(appStarted, "app-fiber")).catch((error: unknown) => error);
  const screen = owner.mount(pending(screenStarted, "screen-fiber")).catch((error: unknown) => error);
  await Effect.runPromise(Deferred.await(appStarted));
  await Effect.runPromise(Deferred.await(screenStarted));
  await owner.dispose();
  expect(await app).toBeDefined();
  expect(await screen).toBeDefined();
  expect(order).toEqual(["screen-fiber", "app-fiber", "app-resource", "layer"]);
});

test("cleanup failure stays observable while remaining resources are always released", async () => {
  const { state, layer } = resources();
  const failure = new Error("screen cleanup defect");
  let appClosed = 0;
  const owner = createAppRuntime(layer, Effect.acquireRelease(Effect.void, () => Effect.sync(() => { appClosed++; })));
  await owner.ready;
  await owner.mount(Effect.acquireRelease(Effect.void, () => Effect.die(failure)));
  const disposal = owner.dispose();
  await expect(disposal).rejects.toBe(failure);
  expect(owner.phase()).toBe("stopped");
  expect(appClosed).toBe(1);
  expect(state.active).toBe(0);
  expect(state.closed).toBe(1);
  expect(owner.dispose()).toBe(disposal);
});

test("initialization and cleanup failures are both retained and cleanup still reaches baseline", async () => {
  const { state, layer } = resources();
  const initialization = new Error("initialize failed");
  const cleanup = new Error("cleanup failed");
  const owner = createAppRuntime(layer, Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.void, () => Effect.die(cleanup));
    return yield* Effect.fail(initialization);
  }));
  const failure: unknown = await owner.ready.catch((error: unknown) => error);
  expect(failure).toBeInstanceOf(AggregateError);
  if (failure instanceof AggregateError) expect(failure.errors).toEqual([initialization, cleanup]);
  expect(state.active).toBe(0);
  expect(state.closed).toBe(1);
  await expect(owner.dispose()).rejects.toBe(cleanup);
});

test("completed startup cannot become running after disposal already began", async () => {
  const { state, layer } = resources();
  const owner = createAppRuntime(layer, Effect.succeed("ready"));
  const ready = owner.ready.catch((error: unknown) => error);
  await owner.dispose();
  expect(await ready).toBeInstanceOf(RuntimeUnavailable);
  expect(owner.phase()).toBe("stopped");
  expect(state.active).toBe(0);
});

test("one hundred independent boots and normal shutdowns retain no Layer resources", async () => {
  const { state, layer } = resources();
  for (let index = 0; index < 100; index++) {
    const owner = createAppRuntime(layer, Effect.void);
    await owner.ready;
    await owner.mount(Effect.acquireRelease(Effect.void, () => Effect.void));
    await owner.dispose();
    expect(state.active).toBe(0);
    expect(state.opened).toBe(index + 1);
    expect(state.closed).toBe(index + 1);
  }
});

function hotContext() {
  const data: RuntimeHotData = {};
  let callback: ((data: RuntimeHotData) => void) | undefined;
  let beforeReload: (() => Promise<void>) | undefined;
  const hot: RuntimeHotContext = { data, dispose: (value) => { callback = value; }, on: (_event, value) => { beforeReload = value; } };
  return { hot, reload: () => { if (beforeReload === undefined) throw new Error("Full reload cleanup absent"); return beforeReload(); }, dispose: () => {
    if (callback === undefined) throw new Error("HMR callback absent");
    callback(data);
  } };
}

test("without HMR the helper returns one Runtime with the same normal lifecycle", async () => {
  const { state, layer } = resources();
  const owner = await createAppRuntimeWithHmr(layer, Effect.succeed("ready"), undefined, () => { throw new Error("unexpected failure"); });
  expect(await owner.ready).toBe("ready");
  await owner.dispose();
  expect(state.active).toBe(0);
});

test("HMR stores disposal and waits for old resources before allocating a replacement Runtime", async () => {
  const { state, layer } = resources();
  const hot = hotContext();
  const closing = Deferred.makeUnsafe<void>();
  const permitClose = Deferred.makeUnsafe<void>();
  const owner = await createAppRuntimeWithHmr(layer, Effect.acquireRelease(Effect.void, () => Effect.gen(function*() {
    yield* Deferred.succeed(closing, undefined);
    yield* Deferred.await(permitClose);
  })), hot.hot, () => { throw new Error("unexpected disposal failure"); });
  await owner.ready;
  hot.dispose();
  expect(hot.hot.data.jackpotPreviousDispose).toBe(owner.dispose());
  await Effect.runPromise(Deferred.await(closing));
  const replacement = createAppRuntimeWithHmr(layer, Effect.void, hot.hot, () => {});
  expect(state).toEqual({ active: 1, opened: 1, closed: 0 });
  Deferred.doneUnsafe(permitClose, Effect.void);
  const next = await replacement;
  await next.ready;
  expect(state).toEqual({ active: 1, opened: 2, closed: 1 });
  await next.dispose();
  expect(state.active).toBe(0);
});

test("HMR cleanup failure reports the error and blocks replacement instead of overlapping runtimes", async () => {
  const { state, layer } = resources();
  const hot = hotContext();
  const reported: unknown[] = [];
  const failure = new Error("HMR dispose failed");
  const owner = await createAppRuntimeWithHmr(layer, Effect.acquireRelease(Effect.void, () => Effect.die(failure)), hot.hot,
    (error) => { reported.push(error); });
  await owner.ready;
  hot.dispose();
  await expect(owner.dispose()).rejects.toBe(failure);
  expect(reported).toEqual([failure]);
  await expect(createAppRuntimeWithHmr(layer, Effect.void, hot.hot, () => {})).rejects.toBe(failure);
  expect(state).toEqual({ active: 0, opened: 1, closed: 1 });
});

test("beforeFullReload awaits pending screen app and Layer cleanup and rejects new work", async () => {
  const { state, layer } = resources(); const hot = hotContext();
  const entered = Deferred.makeUnsafe<void>(); const release = Deferred.makeUnsafe<void>(); const order: string[] = [];
  const owner = await createAppRuntimeWithHmr(layer, Effect.acquireRelease(Effect.void, () => Effect.sync(() => { order.push("app"); })), hot.hot, () => { throw new Error("unexpected cleanup failure"); });
  await owner.ready;
  await owner.mount(Effect.acquireRelease(Effect.void, () => Effect.gen(function*() { order.push("screen"); yield* Deferred.succeed(entered, undefined); yield* Deferred.await(release); })));
  try {
  const reloading = hot.reload(); let finished = false; void reloading.then(() => { finished = true; });
  await Effect.runPromise(Deferred.await(entered));
  expect(owner.phase()).toBe("stopping"); expect(finished).toBe(false); expect(state.active).toBe(1); expect(order).toEqual(["screen"]);
  await expect(owner.run(Effect.void)).rejects.toEqual(new RuntimeUnavailable({ phase: "stopping" }));
  hot.dispose(); expect(hot.hot.data.jackpotPreviousDispose).toBe(owner.dispose());
  Deferred.doneUnsafe(release, Effect.void); await reloading; await owner.dispose();
  expect(finished).toBe(true); expect(order).toEqual(["screen", "app"]); expect(state.active).toBe(0); expect(state.closed).toBe(1);
  } finally { Deferred.doneUnsafe(release, Effect.void); await owner.dispose(); }
});

test("full reload and repeated HMR disposal report one failure and keep replacement blocked", async () => {
  const { state, layer } = resources(); const hot = hotContext(); const failure = new Error("reload cleanup failure"); const reported: unknown[] = [];
  const owner = await createAppRuntimeWithHmr(layer, Effect.acquireRelease(Effect.void, () => Effect.die(failure)), hot.hot, error => { reported.push(error); });
  try { await owner.ready; hot.dispose(); hot.dispose();
  await expect(hot.reload()).rejects.toBe(failure); await expect(hot.reload()).rejects.toBe(failure);
  await expect(createAppRuntimeWithHmr(layer, Effect.void, hot.hot, () => {})).rejects.toBe(failure);
  expect(reported).toEqual([failure]); expect(state).toEqual({ active: 0, opened: 1, closed: 1 }); expect(owner.phase()).toBe("stopped");
  } finally { await owner.dispose().catch(() => {}); }
});

test("one hundred actual hot disposal handoffs retain one Runtime and no prior app or screen resources", async () => {
  const { state, layer } = resources(); const hot = hotContext(); let appActive = 0; let screenActive = 0;
  for (let index = 0; index < 100; index++) {
    const owner = await createAppRuntimeWithHmr(layer, Effect.acquireRelease(Effect.sync(() => { appActive++; }), () => Effect.sync(() => { appActive--; })), hot.hot, () => { throw new Error("unexpected failure"); });
    try { await owner.ready; await owner.mount(Effect.acquireRelease(Effect.sync(() => { screenActive++; }), () => Effect.sync(() => { screenActive--; })));
    expect(state.active).toBe(1); expect(appActive).toBe(1); expect(screenActive).toBe(1);
    hot.dispose(); hot.dispose(); await hot.hot.data.jackpotPreviousDispose;
    expect(state.active).toBe(0); expect(appActive).toBe(0); expect(screenActive).toBe(0); expect(state.opened).toBe(index + 1); expect(state.closed).toBe(index + 1);
    } finally { await owner.dispose(); }
  }
});
