import { expect, it, vi } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { DomError, makeDomPlatform } from "../../src/platform/dom";
import { makeKeyedRows, mountView, patchAria, patchDisabled, patchInputValue, patchText, ViewUnavailable, type RowView } from "../../src/ui/view";

const dom = makeDomPlatform({ request: () => 0, cancel: () => {} }, () => "visible");
interface Row { readonly id: string; readonly label: string }
const data = (id: string, label = id): Row => ({ id, label });

function rowFactory(fail: (call: number) => boolean = () => false) {
  const state = { builds: 0, active: 0, clicks: 0, patches: 0, closed: [] as string[] };
  const nodes = new Map<string, { readonly element: HTMLLIElement; readonly input: HTMLInputElement; readonly button: HTMLButtonElement }>();
  const build = (model: Row): Effect.Effect<RowView<Row>, DomError, Scope.Scope> => Effect.gen(function*() {
    state.builds++;
    yield* Effect.acquireRelease(Effect.sync(() => { state.active++; }), () => Effect.sync(() => { state.active--; state.closed.push(model.id); }));
    const element = document.createElement("li");
    const label = document.createElement("label");
    const input = document.createElement("input");
    const button = document.createElement("button");
    input.id = `row-${model.id}`;
    label.htmlFor = input.id;
    patchText(label, model.label);
    patchInputValue(input, model.label, false);
    patchText(button, "선택");
    button.type = "button";
    element.append(label, input, button);
    yield* dom.listen(button, "click", () => { state.clicks++; });
    nodes.set(model.id, { element, input, button });
    if (fail(state.builds)) return yield* Effect.fail(new DomError());
    return { element, value: { patch: (next) => {
      state.patches++;
      patchText(label, next.label);
      patchInputValue(input, next.label, false);
    } } };
  });
  return { state, nodes, build };
}

function host(): Effect.Effect<HTMLElement, never, Scope.Scope> {
  return Effect.acquireRelease(Effect.sync(() => {
    const element = document.createElement("ul");
    document.body.append(element);
    return element;
  }), (element) => Effect.sync(() => element.remove()));
}

it.effect("same-value patches preserve the exact input node, focus, caret and DOM setter baseline", () => Effect.gen(function*() {
  const container = yield* host();
  const input = document.createElement("input");
  input.value = "한글 abc";
  container.append(input);
  input.focus();
  input.setSelectionRange(2, 4);
  const value = vi.spyOn(input, "value", "set");
  const disabled = vi.spyOn(input, "disabled", "set");
  try {
    for (let index = 0; index < 100; index++) {
      patchInputValue(input, "한글 abc", false);
      patchDisabled(input, false);
    }
    expect(value).not.toHaveBeenCalled();
    expect(disabled).not.toHaveBeenCalled();
    expect(container.firstChild).toBe(input);
    expect(document.activeElement).toBe(input);
    expect([input.selectionStart, input.selectionEnd]).toEqual([2, 4]);
    patchInputValue(input, "변경", true);
    expect(input.value).toBe("한글 abc");
    expect(value).not.toHaveBeenCalled();
    patchInputValue(input, "변경", false);
    patchDisabled(input, true);
    expect(value).toHaveBeenCalledExactlyOnceWith("변경");
    expect(disabled).toHaveBeenCalledExactlyOnceWith(true);
  } finally { value.mockRestore(); disabled.mockRestore(); }
}));

it.effect("text and aria patches are idempotent and external markup stays literal text", () => Effect.gen(function*() {
  const container = yield* host();
  const text = vi.spyOn(container, "textContent", "set");
  const set = vi.spyOn(container, "setAttribute");
  const remove = vi.spyOn(container, "removeAttribute");
  try {
    const hostile = '<script>globalThis.attacked=true</script><img onerror="bad()">';
    patchText(container, hostile);
    patchText(container, hostile);
    expect(text).toHaveBeenCalledTimes(1);
    expect(container.textContent).toBe(hostile);
    expect(container.querySelector("script,img")).toBeNull();
    patchAria(container, "aria-describedby", null);
    expect(remove).not.toHaveBeenCalled();
    patchAria(container, "aria-describedby", "error");
    patchAria(container, "aria-describedby", "error");
    expect(set).toHaveBeenCalledExactlyOnceWith("aria-describedby", "error");
    patchAria(container, "aria-describedby", null);
    patchAria(container, "aria-describedby", null);
    expect(remove).toHaveBeenCalledExactlyOnceWith("aria-describedby");
  } finally { text.mockRestore(); set.mockRestore(); remove.mockRestore(); }
}));

it.effect("a scoped mount adds one owned root and repeated unmount preserves unrelated host nodes", () => Effect.gen(function*() {
  const container = yield* host();
  const baseline = document.createElement("span");
  container.append(baseline);
  const scope = yield* Scope.make();
  let active = 0;
  const mounted = yield* mountView(container, Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; }));
    return { element: document.createElement("section"), value: "view" };
  })).pipe(Scope.provide(scope));
  expect(mounted.value).toBe("view");
  expect(container.children).toHaveLength(2);
  yield* mounted.close;
  yield* mounted.close;
  expect(active).toBe(0);
  expect(container.childNodes).toHaveLength(1);
  expect(container.firstChild).toBe(baseline);
  yield* Scope.close(scope, Exit.void);
}));

for (const failing of [[1], [2], [1, 2, 3]]) {
  it.effect(`mount build failures ${failing.join(",")} roll back resources and retain sibling views`, () => Effect.gen(function*() {
    const container = yield* host();
    const baseline = document.createElement("span");
    container.append(baseline);
    const scope = yield* Scope.make();
    let calls = 0;
    let active = 0;
    for (let index = 1; index <= 3; index++) {
      const result = yield* Effect.result(mountView(container, Effect.gen(function*() {
        calls++;
        yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; }));
        if (failing.includes(calls)) return yield* Effect.fail(new DomError());
        return { element: document.createElement("section"), value: index };
      })).pipe(Scope.provide(scope)));
      expect(result._tag).toBe(failing.includes(index) ? "Failure" : "Success");
    }
    expect(active).toBe(3 - failing.length);
    expect(container.childNodes).toHaveLength(1 + active);
    yield* Scope.close(scope, Exit.void);
    expect(active).toBe(0);
    expect(container.firstChild).toBe(baseline);
    expect(container.childNodes).toHaveLength(1);
  }));
}

it.effect("append failure after a partial native side effect removes the new node and listeners", () => Effect.gen(function*() {
  const container = yield* host();
  const existing = document.createElement("span");
  container.append(existing);
  const scope = yield* Scope.make();
  const root = document.createElement("button");
  let clicks = 0;
  const original = container.append.bind(container);
  const append = vi.spyOn(container, "append").mockImplementation((...nodes) => { original(...nodes); throw new Error("partial append"); });
  try {
    const result = yield* Effect.result(mountView(container, Effect.gen(function*() {
      yield* dom.listen(root, "click", () => { clicks++; });
      return { element: root, value: undefined };
    })).pipe(Scope.provide(scope)));
    expect(result._tag).toBe("Failure");
    expect(root.parentNode).toBeNull();
    root.click();
    expect(clicks).toBe(0);
    expect(container.childNodes).toHaveLength(1);
    expect(container.firstChild).toBe(existing);
  } finally { append.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("mount rejects an already attached root without removing foreign nodes", () => Effect.gen(function*() {
  const container = yield* host();
  const foreign = document.createElement("span");
  container.append(foreign);
  const result = yield* Effect.result(mountView(container, Effect.succeed({ element: foreign, value: undefined })));
  expect(result._tag).toBe("Failure");
  expect(container.firstChild).toBe(foreign);
}));

it.effect("closed consumer Scope rejects mount before build and mount closing during build cannot append later", () => Effect.gen(function*() {
  const container = yield* host();
  const closed = yield* Scope.make();
  yield* Scope.close(closed, Exit.void);
  let builds = 0;
  const result = yield* Effect.result(mountView(container, Effect.sync(() => { builds++; return { element: document.createElement("div"), value: undefined }; })).pipe(Scope.provide(closed)));
  expect(result._tag).toBe("Failure");
  expect(builds).toBe(0);
  const scope = yield* Scope.make();
  const entered = yield* Deferred.make<void>();
  const continueBuild = yield* Deferred.make<void>();
  const fiber = yield* mountView(container, Effect.gen(function*() {
    yield* Deferred.succeed(entered, undefined);
    yield* Deferred.await(continueBuild);
    return { element: document.createElement("div"), value: undefined };
  })).pipe(Scope.provide(scope), Effect.result, Effect.forkChild);
  yield* Deferred.await(entered);
  yield* Scope.close(scope, Exit.void);
  yield* Deferred.succeed(continueBuild, undefined);
  expect((yield* Fiber.join(fiber))._tag).toBe("Failure");
  expect(container.childNodes).toHaveLength(0);
}));

it.effect("keyed row updates preserve node identity, focus, raw value and native label association", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const fixture = rowFactory();
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope));
  yield* rows.patch([data("a", "한글 abc"), data("b")]);
  const original = fixture.nodes.get("a");
  if (original === undefined) throw new Error("row missing");
  original.input.focus();
  original.input.setSelectionRange(1, 3);
  const setter = vi.spyOn(original.input, "value", "set");
  const insert = vi.spyOn(container, "insertBefore");
  try {
    yield* rows.patch([data("a", "한글 abc"), data("b")]);
    expect(fixture.state.builds).toBe(2);
    expect(container.firstChild).toBe(original.element);
    expect(document.activeElement).toBe(original.input);
    expect([original.input.selectionStart, original.input.selectionEnd]).toEqual([1, 3]);
    expect(setter).not.toHaveBeenCalled();
    expect(insert).not.toHaveBeenCalled();
    expect(original.element.querySelector("label")?.htmlFor).toBe(original.input.id);
    expect(original.button.type).toBe("button");
    yield* rows.patch([data("a", "새 값"), data("b")]);
    expect(setter).toHaveBeenCalledExactlyOnceWith("새 값");
  } finally { setter.mockRestore(); insert.mockRestore(); yield* Scope.close(scope, Exit.void); }
  expect(container.childNodes).toHaveLength(0);
  expect(fixture.state.active).toBe(0);
}));

it.effect("removing keyed rows closes only their listeners while surviving rows and order remain valid", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const fixture = rowFactory();
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope));
  yield* rows.patch([data("a"), data("b"), data("c")]);
  const a = fixture.nodes.get("a");
  const b = fixture.nodes.get("b");
  const c = fixture.nodes.get("c");
  if (a === undefined || b === undefined || c === undefined) throw new Error("rows missing");
  yield* rows.patch([data("c"), data("a")]);
  expect([...container.children]).toEqual([c.element, a.element]);
  expect(fixture.state.active).toBe(2);
  expect(fixture.state.closed).toEqual(["b"]);
  b.button.click();
  expect(fixture.state.clicks).toBe(0);
  a.button.click();
  c.button.click();
  expect(fixture.state.clicks).toBe(2);
  yield* rows.patch([]);
  expect(container.childNodes).toHaveLength(0);
  expect(fixture.state.active).toBe(0);
  yield* Scope.close(scope, Exit.void);
  expect((yield* Effect.result(rows.patch([])))._tag).toBe("Failure");
}));

for (const maximum of [0, -1, 1.5, NaN, Infinity, 101, Number.MAX_SAFE_INTEGER]) {
  it.effect(`invalid row maximum ${maximum} cannot acquire a list or alter the host`, () => Effect.gen(function*() {
    const container = yield* host();
    const fixture = rowFactory();
    expect((yield* Effect.result(makeKeyedRows(container, maximum, (model: Row) => model.id, fixture.build)))._tag).toBe("Failure");
    expect(fixture.state.builds).toBe(0);
    expect(container.childNodes).toHaveLength(0);
  }));
}

it.effect("row host precondition preserves unrelated children and closed Scope rejects construction", () => Effect.gen(function*() {
  const container = yield* host();
  const foreign = document.createTextNode("외부 노드");
  container.append(foreign);
  const fixture = rowFactory();
  expect((yield* Effect.result(makeKeyedRows(container, 1, (model: Row) => model.id, fixture.build)))._tag).toBe("Failure");
  expect(container.firstChild).toBe(foreign);
  container.replaceChildren();
  const scope = yield* Scope.make();
  yield* Scope.close(scope, Exit.void);
  expect((yield* Effect.result(makeKeyedRows(container, 1, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope))))._tag).toBe("Failure");
}));

for (const models of [[data("")], [data("a"), data("a")], [data(null as never)], [data(0 as never)]]) {
  it.effect(`invalid or duplicate row identities reject before creating nodes: ${JSON.stringify(models)}`, () => Effect.gen(function*() {
    const container = yield* host();
    const fixture = rowFactory();
    const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build);
    yield* rows.patch([data("original")]);
    expect((yield* Effect.result(rows.patch(models)))._tag).toBe("Failure");
    expect(fixture.state.builds).toBe(1);
    expect(container.firstChild?.textContent).toBe("original선택");
    expect(fixture.state.active).toBe(1);
  }));
}

it.effect("row count boundaries zero, one, one hundred and overflow preserve the authoritative view", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const fixture = rowFactory();
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope));
  yield* rows.patch([]);
  yield* rows.patch([data("0")]);
  const original = container.firstChild;
  const hundred = Array.from({ length: 100 }, (_, index) => data(String(index)));
  yield* rows.patch(hundred);
  expect(container.children).toHaveLength(100);
  expect(container.firstChild).toBe(original);
  expect((yield* Effect.result(rows.patch([...hundred, data("overflow")])))._tag).toBe("Failure");
  expect(container.children).toHaveLength(100);
  expect(fixture.state.builds).toBe(100);
  yield* Scope.close(scope, Exit.void);
  expect(fixture.state.active).toBe(0);
}));

for (const failing of [[2], [3], [2, 3, 4]]) {
  it.effect(`staged row failure ${failing.join(",")} removes partial nodes and retains existing rows`, () => Effect.gen(function*() {
    const container = yield* host();
    const scope = yield* Scope.make();
    const fixture = rowFactory((call) => failing.includes(call));
    const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope));
    yield* rows.patch([data("original")]);
    const original = container.firstChild;
    expect((yield* Effect.result(rows.patch([data("original", "미승인 변경"), data("a"), data("b")])))._tag).toBe("Failure");
    expect(container.childNodes).toHaveLength(1);
    expect(container.firstChild).toBe(original);
    expect(container.textContent).toBe("original선택");
    expect(fixture.state.active).toBe(1);
    expect(fixture.state.patches).toBe(0);
    yield* Scope.close(scope, Exit.void);
    expect(fixture.state.active).toBe(0);
  }));
}

it.effect("render or native commit failures close the list instead of leaving partially committed rows", () => Effect.gen(function*() {
  for (const failure of ["patch", "insert"]) {
    const container = yield* host();
    const scope = yield* Scope.make();
    const fixture = rowFactory();
    const build = (model: Row) => fixture.build(model).pipe(Effect.map((row) => ({ ...row, value: {
      patch: (next: Row) => { row.value.patch(next); if (failure === "patch") throw new Error("render failed"); },
    } })));
    const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, build).pipe(Scope.provide(scope));
    yield* rows.patch([data("a")]);
    const original = container.insertBefore.bind(container);
    const insert = failure === "insert" ? vi.spyOn(container, "insertBefore").mockImplementation((node, before) => {
      original(node, before);
      throw new Error("partial insert");
    }) : undefined;
    try {
      expect((yield* Effect.result(rows.patch([data("a", "changed"), data("b")])))._tag).toBe("Failure");
      expect(container.childNodes).toHaveLength(0);
      expect(fixture.state.active).toBe(0);
      const retry = yield* Effect.result(rows.patch([]));
      expect(retry._tag).toBe("Failure");
      if (retry._tag === "Failure") expect(retry.failure).toBeInstanceOf(ViewUnavailable);
    } finally { insert?.mockRestore(); yield* Scope.close(scope, Exit.void); }
  }
}));

it.effect("a parent closing just after staged append blocks commit and retains no staged node", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const fixture = rowFactory();
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, fixture.build).pipe(Scope.provide(scope));
  const original = container.ownerDocument.createDocumentFragment.bind(container.ownerDocument);
  const create = vi.spyOn(container.ownerDocument, "createDocumentFragment").mockImplementation(() => {
    const fragment = original();
    const append = fragment.append.bind(fragment);
    fragment.append = (...nodes) => {
      append(...nodes);
      // Native/custom-element callbacks may close a view during an append.
      Effect.runSync(Scope.close(scope, Exit.void));
    };
    return fragment;
  });
  try {
    const result = yield* Effect.result(rows.patch([data("a")]));
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
    expect(container.childNodes).toHaveLength(0);
    expect(fixture.state.active).toBe(0);
  } finally { create.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("parent closing during row cleanup prevents a late commit from retaining closed views", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const removing = yield* Deferred.make<void>();
  const release = yield* Deferred.make<void>();
  const fixture = rowFactory();
  const build = (model: Row) => Effect.gen(function*() {
    if (model.id === "old") yield* Effect.addFinalizer(() => Effect.gen(function*() {
      yield* Deferred.succeed(removing, undefined);
      yield* Deferred.await(release);
    }));
    return yield* fixture.build(model);
  });
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, build).pipe(Scope.provide(scope));
  yield* rows.patch([data("old")]);
  const patch = yield* rows.patch([data("new")]).pipe(Effect.result, Effect.forkChild);
  try {
    const boundary = yield* Effect.raceFirst(
      Deferred.await(removing).pipe(Effect.as("removing")),
      Fiber.await(patch).pipe(Effect.as("completed")),
    );
    expect(boundary).toBe("removing");
    yield* Scope.close(scope, Exit.void);
    yield* Deferred.succeed(release, undefined);
    const result = yield* Fiber.join(patch);
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
    expect(container.childNodes).toHaveLength(0);
    expect(fixture.state.active).toBe(0);
    expect((yield* Effect.result(rows.patch([])))._tag).toBe("Failure");
  } finally { yield* Deferred.succeed(release, undefined); yield* Scope.close(scope, Exit.void); }
}));

it.effect("concurrent list patches serialize their staged builders and create no duplicate key", () => Effect.gen(function*() {
  const container = yield* host();
  const scope = yield* Scope.make();
  const entered = yield* Deferred.make<void>();
  const release = yield* Deferred.make<void>();
  const fixture = rowFactory();
  let starts = 0;
  const build = (model: Row) => Effect.gen(function*() {
    starts++;
    yield* Deferred.succeed(entered, undefined);
    yield* Deferred.await(release);
    return yield* fixture.build(model);
  });
  const rows = yield* makeKeyedRows(container, 100, (model: Row) => model.id, build).pipe(Scope.provide(scope));
  const first = yield* rows.patch([data("same", "first")]).pipe(Effect.forkChild);
  yield* Deferred.await(entered);
  const secondStarted = yield* Deferred.make<void>();
  const second = yield* Effect.gen(function*() {
    yield* Deferred.succeed(secondStarted, undefined);
    yield* rows.patch([data("same", "second")]);
  }).pipe(Effect.forkChild);
  yield* Deferred.await(secondStarted);
  expect(starts).toBe(1);
  yield* Deferred.succeed(release, undefined);
  yield* Fiber.join(first);
  yield* Fiber.join(second);
  expect(starts).toBe(1);
  expect(fixture.state.builds).toBe(1);
  expect(container.textContent).toBe("second선택");
  expect(container.childNodes).toHaveLength(1);
  yield* Scope.close(scope, Exit.void);
  expect(fixture.state.active).toBe(0);
}));
