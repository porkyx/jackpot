import { ProtocolError } from "../contracts/backend";

export const MaximumQueryBytes = 8 * 1024 * 1024;

// Wails supplies decoded JSON. Count the encoding/json HTML-escaped UTF-8
// representation before Schema walks it, without allocating another large JSON.
export function assertQuerySize(raw: unknown, maximum = MaximumQueryBytes): void {
  if (!Number.isSafeInteger(maximum) || maximum < 0) throw new ProtocolError();
  let bytes = 0;
  const active = new Set<object>();
  const add = (count: number) => { bytes += count; if (bytes > maximum) throw new ProtocolError(); };
  const string = (value: string) => {
    add(2);
    if (!/[\u0000-\u001f"\\<>&\u007f-\uffff]/.test(value)) { add(value.length); return; }
    for (let i = 0; i < value.length; i++) {
      const code = value.charCodeAt(i);
      if (code === 34 || code === 92 || code === 8 || code === 9 || code === 10 || code === 12 || code === 13) add(2);
      else if (code < 32 || code === 38 || code === 60 || code === 62 || code === 0x2028 || code === 0x2029) add(6);
      else if (code >= 0xd800 && code <= 0xdbff && i + 1 < value.length && value.charCodeAt(i + 1) >= 0xdc00 && value.charCodeAt(i + 1) <= 0xdfff) { add(4); i++; }
      else if (code >= 0xd800 && code <= 0xdfff) add(6);
      else add(code < 128 ? 1 : code < 2048 ? 2 : 3);
    }
  };
  const visit = (value: unknown, depth: number): void => {
    if (depth > 64) throw new ProtocolError();
    if (value === null) { add(4); return; }
    switch (typeof value) {
      case "string": string(value); return;
      case "boolean": add(value ? 4 : 5); return;
      case "number": if (!Number.isFinite(value)) throw new ProtocolError(); add(JSON.stringify(value).length); return;
      case "object": {
        if (active.has(value)) throw new ProtocolError();
        active.add(value); add(2); let count = 0;
        if (Array.isArray(value)) {
          for (const item of value) { if (count++ > 0) add(1); visit(item, depth + 1); }
        } else {
          for (const key in value) {
            if (!Object.hasOwn(value, key)) continue;
            const item: unknown = (value as Record<string, unknown>)[key];
            if (item === undefined) continue;
            if (count++ > 0) add(1); string(key); add(1); visit(item, depth + 1);
          }
        }
        active.delete(value); return;
      }
      default: throw new ProtocolError();
    }
  };
  visit(raw, 0);
}
