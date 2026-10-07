import { DomError } from "../../platform/dom";
import { patchAria, patchText } from "../view";

export type NativeField = HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement;

// Each field owns its error description; caller-owned hints need separate IDs.
export function connectField(label: HTMLLabelElement, input: NativeField, error: HTMLElement): void {
  if (input.id.length === 0 || error.id.length === 0 || input.id === error.id) throw new DomError();
  if (label.htmlFor !== input.id) label.htmlFor = input.id;
  if (error.getAttribute("role") !== "alert") error.setAttribute("role", "alert");
}

export function patchFieldError(input: NativeField, error: HTMLElement, message: string | null): void {
  if (error.id.length === 0 || (message !== null && message.trim().length === 0)) throw new DomError();
  patchText(error, message ?? "");
  patchAria(input, "aria-invalid", message === null ? null : "true");
  patchAria(input, "aria-describedby", message === null ? null : error.id);
  if (error.hidden !== (message === null)) error.hidden = message === null;
}
