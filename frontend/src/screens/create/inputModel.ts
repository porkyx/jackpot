export type InputScreenModel = { readonly _tag: "closed" } | {
  readonly _tag: "open";
  readonly composition: "idle" | "composing" | "ended";
  readonly focus: { readonly start: number; readonly end: number } | null;
};
export type InputScreenAction = { readonly _tag: "CompositionStarted" | "CompositionEnded" | "OtherKey" | "Blurred" | "Closed" }
  | { readonly _tag: "Focused"; readonly start: number; readonly end: number };
export function createInputScreen(): InputScreenModel { return Object.freeze({ _tag: "open", composition: "idle", focus: null }); }
export function reduceInputScreen(model: InputScreenModel, action: InputScreenAction): InputScreenModel {
  if (model._tag === "closed") return model;
  switch (action._tag) {
    case "CompositionStarted": return Object.freeze({ ...model, composition: "composing" });
    case "CompositionEnded": return Object.freeze({ ...model, composition: "ended" });
    case "OtherKey": return Object.freeze({ ...model, composition: model.composition === "ended" ? "idle" : model.composition });
    case "Blurred": return Object.freeze({ ...model, focus: null });
    case "Focused": {
      if (!Number.isSafeInteger(action.start) || !Number.isSafeInteger(action.end) || action.start < 0 || action.end < action.start) throw new Error("Caret selection is invalid");
      return Object.freeze({ ...model, focus: Object.freeze({ start: action.start, end: action.end }) });
    }
    case "Closed": return Object.freeze({ _tag: "closed" });
    default: throw new Error(`Unknown input screen action: ${String(action)}`);
  }
}
export function decideInputEnter(model: InputScreenModel, eventIsComposing: boolean, keyCode: number): { readonly model: InputScreenModel; readonly submit: boolean } {
  if (model._tag === "closed" || model.composition === "composing" || eventIsComposing || keyCode === 229) return { model, submit: false };
  if (model.composition === "ended") return { model: reduceInputScreen(model, { _tag: "OtherKey" }), submit: false };
  return { model, submit: true };
}
