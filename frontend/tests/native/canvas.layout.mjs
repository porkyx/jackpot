// Test-only passive probe. It forwards the original Canvas objects, methods,
// arguments, callback receiver and failures; it never changes product layout.
export function installCanvasLayoutProbe({ maxRecords = 16, maxTrackedUnits = 262144, capturePaint = false } = {}, surface = globalThis) {
  if (!Number.isSafeInteger(maxRecords) || maxRecords < 1 || maxRecords > 64 || !Number.isSafeInteger(maxTrackedUnits) || maxTrackedUnits < 0 || maxTrackedUnits > 1048576 || typeof capturePaint !== "boolean") throw new RangeError("CanvasLayoutProbeBounds");
  if (surface.__jackpotCanvasLayoutProbe !== undefined) throw new RangeError("CanvasLayoutProbeAlreadyInstalled");
  const canvasPrototype = surface.HTMLCanvasElement.prototype;
  const contextPrototype = surface.CanvasRenderingContext2D.prototype;
  const originalGetContext = canvasPrototype.getContext;
  const originalToBlob = canvasPrototype.toBlob;
  const originalMeasure = contextPrototype.measureText;
  const originalFill = contextPrototype.fillText;
  let alive = true, sequence = 0, observerFaults = 0;
  let contexts = new WeakMap(), canvases = new WeakMap();
  const records = [];
  const now = () => { try { const value = surface.performance.now(); return Number.isFinite(value) ? value : null; } catch { observerFaults++; return null; } };
  const safe = action => { try { action(); } catch { observerFaults++; } };
  const begin = canvas => {
    const entry = { sequence: ++sequence, calls: 0, uniqueMeasured: 0, duplicateMeasured: 0, measuredUTF16: 0, maxPrefixUTF16: 0, trackedUnits: 0, trackingTruncated: false, measureMs: 0, measureFailures: 0, fillCalls: 0, fillMs: 0, fillFailures: 0, toBlobCalls: 0, toBlobFailures: 0, callbacks: 0, encodingMs: 0, blobBytes: 0, blobType: "", width: 0, height: 0, pixelsReleased: false, ...(capturePaint ? { paints: [], paintUnits: 0, paintTrackingTruncated: false } : {}), texts: new Set(), canvas: new WeakRef(canvas) };
    records.push(entry);
    while (records.length > maxRecords) { const removed = records.shift(); removed.texts.clear(); removed.paints?.splice(0); }
    canvases.set(canvas, entry);
    return entry;
  };
  const getContext = function (...args) {
    const result = Reflect.apply(originalGetContext, this, args);
    if (alive && args[0] === "2d" && result !== null) safe(() => { const entry = canvases.get(this) ?? begin(this); contexts.set(result, entry); });
    return result;
  };
  const measureText = function (...args) {
    const entry = alive ? contexts.get(this) : undefined;
    const started = entry === undefined ? null : now();
    let failed = false;
    try { return Reflect.apply(originalMeasure, this, args); }
    catch (error) { failed = true; throw error; }
    finally { if (entry !== undefined) safe(() => {
      entry.calls++; if (failed) entry.measureFailures++;
      const finished = now(); if (started !== null && finished !== null) entry.measureMs += Math.max(0, finished - started);
      if (typeof args[0] === "string") {
        const text = args[0]; entry.measuredUTF16 += text.length; entry.maxPrefixUTF16 = Math.max(entry.maxPrefixUTF16, text.length);
        if (entry.texts.has(text)) entry.duplicateMeasured++;
        else if (entry.trackedUnits + text.length <= maxTrackedUnits) { entry.texts.add(text); entry.uniqueMeasured++; entry.trackedUnits += text.length; }
        else entry.trackingTruncated = true;
      }
    }); }
  };
  const fillText = function (...args) {
    const entry = alive ? contexts.get(this) : undefined;
    const started = entry === undefined ? null : now(); let failed = false;
    try { return Reflect.apply(originalFill, this, args); }
    catch (error) { failed = true; throw error; }
    finally { if (entry !== undefined) safe(() => {
      entry.fillCalls++; if (failed) entry.fillFailures++;
      const finished = now(); if (started !== null && finished !== null) entry.fillMs += Math.max(0, finished - started);
      if (capturePaint && !failed) {
        if (typeof args[0] !== "string" || entry.paints.length >= 512 || entry.paintUnits + args[0].length > maxTrackedUnits) { entry.paintTrackingTruncated = true; return; }
        // Native metrics use the exact font, alignment and baseline at fillText.
        // This extra observation does not enter the product measureText counter.
        const metric = Reflect.apply(originalMeasure, this, [args[0]]);
        const x = args[1], y = args[2];
        const bounds = { left: x - metric.actualBoundingBoxLeft, right: x + metric.actualBoundingBoxRight, top: y - metric.actualBoundingBoxAscent, bottom: y + metric.actualBoundingBoxDescent };
        if (![x, y, metric.width, ...Object.values(bounds)].every(Number.isFinite) || args.length > 3) throw new RangeError("CanvasPaintMetrics");
        entry.paints.push({ text: args[0], x, y, font: this.font, baseline: this.textBaseline, align: this.textAlign, color: this.fillStyle, measuredWidth: metric.width, ...bounds });
        entry.paintUnits += args[0].length;
      }
    }); }
  };
  const toBlob = function (...args) {
    const entry = alive ? canvases.get(this) : undefined;
    if (entry === undefined || typeof args[0] !== "function") return Reflect.apply(originalToBlob, this, args);
    const started = now(); const callback = args[0];
    safe(() => { entry.toBlobCalls++; entry.width = this.width; entry.height = this.height; entry.texts.clear(); });
    const forwarded = [...args];
    forwarded[0] = function (...callbackArgs) {
      if (alive) safe(() => { entry.callbacks++; const finished = now(); if (started !== null && finished !== null) entry.encodingMs += Math.max(0, finished - started); const blob = callbackArgs[0]; if (blob !== null && typeof blob === "object") { entry.blobBytes = blob.size; entry.blobType = blob.type; } });
      return Reflect.apply(callback, this, callbackArgs);
    };
    try { return Reflect.apply(originalToBlob, this, forwarded); }
    catch (error) { safe(() => { entry.toBlobFailures++; }); throw error; }
  };
  const snapshot = () => ({ alive, observerFaults, totalContexts: sequence, records: records.map(entry => {
    const { texts: _texts, canvas: reference, ...publicRecord } = entry;
    const canvas = reference.deref(); publicRecord.pixelsReleased = canvas === undefined || canvas.width * canvas.height === 0;
    if (entry.paints !== undefined) publicRecord.paints = entry.paints.map(paint => ({ ...paint }));
    return publicRecord;
  }) });
  const control = { snapshot, reset: () => { for (const entry of records) { entry.texts.clear(); entry.paints?.splice(0); } records.length = 0; contexts = new WeakMap(); canvases = new WeakMap(); observerFaults = 0; sequence = 0; }, dispose: () => {
    if (!alive) return; alive = false;
    if (canvasPrototype.getContext === getContext) canvasPrototype.getContext = originalGetContext;
    if (canvasPrototype.toBlob === toBlob) canvasPrototype.toBlob = originalToBlob;
    if (contextPrototype.measureText === measureText) contextPrototype.measureText = originalMeasure;
    if (contextPrototype.fillText === fillText) contextPrototype.fillText = originalFill;
    for (const entry of records) { entry.texts.clear(); entry.paints?.splice(0); } records.length = 0; contexts = new WeakMap(); canvases = new WeakMap();
    if (surface.__jackpotCanvasLayoutProbe === control) delete surface.__jackpotCanvasLayoutProbe;
  } };
  canvasPrototype.getContext = getContext; canvasPrototype.toBlob = toBlob;
  contextPrototype.measureText = measureText; contextPrototype.fillText = fillText;
  surface.__jackpotCanvasLayoutProbe = control;
  return control;
}

// Only public controlled fixture paint is opted into this test-only oracle.
// Exact native glyph ink boxes are compared; touching edges are not overlap.
export function readResultCanvasFont(font) {
  const match = typeof font === "string" ? /^(?:(400|600|700|normal|bold)\s+)?(31|35)px\s+"Jackpot Result"(?:,|$)/.exec(font) : null;
  if (match === null) throw new RangeError("CanvasPaintFont");
  const weight = match[1] === "bold" ? 700 : match[1] === undefined || match[1] === "normal" ? 400 : Number(match[1]);
  const size = Number(match[2]);
  if (weight === 400 ? size !== 31 : size !== 35) throw new RangeError("CanvasPaintFont");
  return { weight, size };
}

export function findResultSummaryPaint(paints, participantCount, winnerCount) {
  const text = `참가자 ${participantCount}명 · 당첨자 ${winnerCount}명`;
  const matches = paints.flatMap((paint, index) => paint.text === text && readResultCanvasFont(paint.font).weight === 600 ? [index] : []);
  if (matches.length !== 1) throw new RangeError("CanvasPaintSummary");
  return matches[0];
}

export function verifyCanvasPaintBounds(record) {
  if (!Number.isSafeInteger(record.width) || record.width < 1 || !Number.isSafeInteger(record.height) || record.height < 1 || record.paintTrackingTruncated !== false || !Array.isArray(record.paints) || record.paints.length !== record.fillCalls || record.paints.length === 0) throw new RangeError("CanvasPaintIncomplete");
  const ink = [];
  for (const paint of record.paints) {
    if (typeof paint.text !== "string" || ![paint.left, paint.right, paint.top, paint.bottom, paint.measuredWidth].every(Number.isFinite) || paint.measuredWidth < 0 || paint.right < paint.left || paint.bottom < paint.top) throw new RangeError("CanvasPaintInvalidBounds");
    if (paint.left < -0.5 || paint.top < -0.5 || paint.right > record.width + 0.5 || paint.bottom > record.height + 0.5) throw new RangeError("CanvasPaintClipped");
    if (paint.right > paint.left && paint.bottom > paint.top) ink.push(paint);
  }
  for (let left = 0; left < ink.length; left++) for (let right = left + 1; right < ink.length; right++) {
    if (Math.min(ink[left].right, ink[right].right) - Math.max(ink[left].left, ink[right].left) > 0.5 && Math.min(ink[left].bottom, ink[right].bottom) - Math.max(ink[left].top, ink[right].top) > 0.5) throw new RangeError("CanvasPaintOverlap");
  }
  return { textCalls: record.paints.length, inkBoxes: ink.length, width: record.width, height: record.height, clipped: 0, overlaps: 0 };
}
