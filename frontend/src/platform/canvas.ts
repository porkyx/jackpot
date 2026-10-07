import { Context, Data, Effect } from "effect";
import "./result-image.css";

export class ResultImageError extends Data.TaggedError("ResultImageError")<{
  readonly reason: "invalid-model" | "font" | "dimensions" | "encoding" | "size";
}> {}

export interface ResultExportWinner {
  readonly participantId: string;
  readonly nickname: string;
  readonly publicIdentifier: string;
  readonly participantKind: "registered" | "semi-registered" | "anonymous";
}
export interface ResultExportPrize {
  readonly prizeId: string;
  readonly name: string;
  readonly winners: ReadonlyArray<ResultExportWinner>;
}
export interface ResultExportModel {
  readonly collectionId: string;
  readonly roundId: string;
  readonly roundNumber: number;
  readonly title: string;
  readonly galleryName: string;
  readonly articleUrl: string;
  readonly participantCount: number;
  readonly prizes: ReadonlyArray<ResultExportPrize>;
  readonly message: string;
  readonly executedAt: string;
  readonly scheduledAt: string | null;
  readonly delayed: boolean;
}
export class ResultImage extends Context.Service<ResultImage, {
  readonly renderPng: (model: ResultExportModel) => Effect.Effect<Blob, ResultImageError>;
}>()("jackpot/ResultImage") {}

export const RESULT_PNG_LIMIT = 25 * 1024 * 1024;
export const RESULT_CANVAS_WIDTH = 1200;
const FONT_FAMILY = '"Jackpot Result", "Malgun Gothic", "맑은 고딕", sans-serif';
const FONT = `400 31px ${FONT_FAMILY}`;
const HEADING_FONT = `600 35px ${FONT_FAMILY}`;
const TITLE_FONT = `700 35px ${FONT_FAMILY}`;
export interface ResultCanvasHost {
  readonly createCanvas: () => HTMLCanvasElement;
  readonly fontsReady: () => Promise<unknown>;
  readonly width?: number;
}
function invalid(reason: ResultImageError["reason"]): ResultImageError { return new ResultImageError({ reason }); }
function stamp(value: string): string {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) throw invalid("invalid-model");
  return new Intl.DateTimeFormat("ko-KR", { timeZone: "Asia/Seoul", dateStyle: "medium", timeStyle: "medium" }).format(time);
}
function validate(model: ResultExportModel): ResultExportModel {
  const identifier = /^[A-Za-z0-9_-]{1,128}$/;
  if (!identifier.test(model.collectionId) || !identifier.test(model.roundId) ||
    !Number.isSafeInteger(model.roundNumber) || model.roundNumber < 1 ||
    !Number.isSafeInteger(model.participantCount) || model.participantCount < 0 || model.participantCount > 100000 ||
    model.prizes.length < 1 || model.prizes.length > 10 || model.prizes.reduce((count, prize) => count + prize.winners.length, 0) > 10) throw invalid("invalid-model");
  for (const value of [model.title, model.galleryName, model.articleUrl, model.message]) if (typeof value !== "string" || value.length > 65536) throw invalid("invalid-model");
  const url = new URL(model.articleUrl);
  if (url.protocol !== "https:" || url.hostname !== "gall.dcinside.com" && url.hostname !== "m.dcinside.com" || url.username !== "" || url.password !== "" || url.port !== "") throw invalid("invalid-model");
  stamp(model.executedAt); if (model.scheduledAt !== null) stamp(model.scheduledAt);
  const participants = new Set<string>(); const prizes = new Set<string>();
  for (const prize of model.prizes) {
    if (!identifier.test(prize.prizeId) || prizes.has(prize.prizeId) || prize.name.length > 20 || prize.winners.length < 1) throw invalid("invalid-model");
    prizes.add(prize.prizeId);
    for (const winner of prize.winners) {
      if (!identifier.test(winner.participantId) || participants.has(winner.participantId) || winner.nickname.length === 0 || winner.nickname.length > 4096 ||
        winner.publicIdentifier.length > 4096 || !["registered", "semi-registered", "anonymous"].includes(winner.participantKind)) throw invalid("invalid-model");
      participants.add(winner.participantId);
    }
  }
  return Object.freeze({ ...model, prizes: Object.freeze(model.prizes.map((prize) => Object.freeze({ ...prize, winners: Object.freeze(prize.winners.map((winner) => Object.freeze({ ...winner }))) }))) });
}
export function resultExportText(input: ResultExportModel): string {
  const model = validate(input);
  const lines = [model.title, `갤러리: ${model.galleryName}`, `원본: ${model.articleUrl}`,
    `묶음: ${model.collectionId}`, `회차: ${model.roundNumber} (${model.roundId})`, `참가자: ${model.participantCount}명`];
  if (model.scheduledAt !== null) lines.push(`예정: ${stamp(model.scheduledAt)} (한국 시간)`);
  lines.push(`실행: ${stamp(model.executedAt)} (한국 시간)`);
  if (model.delayed) lines.push("지연 실행");
  for (const [prizeIndex,prize] of model.prizes.entries()) {
    lines.push(`품목: ${prize.name || (model.prizes.length===1?"기본 상품":"상품 "+String.fromCharCode(65+prizeIndex))}`);
    for (const winner of prize.winners) lines.push(`- ${winner.nickname} (${winner.publicIdentifier || winner.participantKind})`);
  }
  if (model.message !== "") lines.push(`관리 메시지: ${model.message}`);
  return lines.join("\n");
}
export function wrapResultText(context: CanvasRenderingContext2D, text: string, width: number): ReadonlyArray<string> {
  if (!Number.isFinite(width) || width <= 0) throw invalid("dimensions");
  const segmenter = new Intl.Segmenter("ko", { granularity: "grapheme" });
  const result: string[] = [];
  for (const paragraph of text.split("\n")) {
    let line = "";
    for (const part of segmenter.segment(paragraph)) {
      const candidate = line + part.segment;
      const measured = context.measureText(candidate).width;
      if (!Number.isFinite(measured) || measured < 0) throw invalid("font");
      if (measured > width) {
        if (line === "") throw invalid("dimensions");
        result.push(line); line = part.segment;
        const fragment = context.measureText(line).width;
        if (!Number.isFinite(fragment) || fragment < 0) throw invalid("font");
        if (fragment > width) throw invalid("dimensions");
      } else line = candidate;
      if (result.length > 224) throw invalid("dimensions");
    }
    result.push(line);
    if (result.length > 224) throw invalid("dimensions");
  }
  return result;
}
interface CanvasText { readonly text: string; readonly x: number; readonly y: number; readonly font: string; readonly color: string; }
interface CanvasCard { readonly x: number; readonly y: number; readonly width: number; readonly height: number; readonly fill: string; readonly border?: string; readonly radius: number; }
function layoutResult(context: CanvasRenderingContext2D, model: ResultExportModel, width: number) {
  const text: CanvasText[] = [], cards: CanvasCard[] = [];
  const foreground = "#1c1e2d", muted = "#6e7080", primary = "#806ab9";
  const inset = 35, contentWidth = width - inset * 2;
  let lineCount = 0;
  const lines = (value: string, font: string, available: number) => {
    context.font = font;
    const wrapped = wrapResultText(context, value, available);
    lineCount += wrapped.length;
    if (lineCount > 224) throw invalid("dimensions");
    return wrapped;
  };
  const drawLines = (wrapped: ReadonlyArray<string>, x: number, y: number, font: string, color: string, height = 44) => {
    wrapped.forEach((value, index) => text.push({ text: value, x, y: y + index * height, font, color }));
    return wrapped.length * height;
  };
  const paragraph = (value: string, y: number, font = FONT, color = foreground) => drawLines(lines(value, font, contentWidth), inset, y, font, color);
  let y = inset;
  y += paragraph(model.title, y, TITLE_FONT);
  const article = new URL(model.articleUrl);
  const articleNumber = article.searchParams.get("no") ?? article.pathname.match(/\/(\d+)\/?$/)?.[1];
  y += paragraph(model.galleryName + (articleNumber === undefined || articleNumber === null ? "" : ` · 게시글 #${articleNumber}`), y, FONT, muted);
  y += 35;
  const cardTop = y, cardInset = inset + 35, cardWidth = contentWidth - 70;
  y += 35;
  y += drawLines(lines(`${model.roundNumber}회차 추첨 결과`, HEADING_FONT, cardWidth), cardInset, y, HEADING_FONT, primary);
  if (model.message !== "") y += drawLines(lines(model.message, FONT, cardWidth), cardInset, y, FONT, muted);
  y += 26;
  const columns = cardWidth >= 400, gap = 26;
  const badgeWidth = columns ? Math.min(280, Math.floor(cardWidth * 0.34)) : cardWidth;
  const winnerWidth = columns ? cardWidth - badgeWidth - gap : cardWidth;
  for (const [prizeIndex, prize] of model.prizes.entries()) {
    const name = prize.name || (model.prizes.length === 1 ? "기본 상품" : "상품 " + String.fromCharCode(65 + prizeIndex));
    for (const [winnerIndex, winner] of prize.winners.entries()) {
      const badge = lines(name, FONT, badgeWidth - 24);
      const identity = lines(`${prize.winners.length > 1 ? `${winnerIndex + 1}. ` : ""}${winner.nickname} (${winner.publicIdentifier || winner.participantKind})`, FONT, winnerWidth);
      const badgeHeight = badge.length * 44 + 12;
      const rowHeight = columns ? Math.max(badge.length, identity.length) * 44 + 12 : badgeHeight + identity.length * 44 + 12;
      cards.push({ x: cardInset, y, width: badgeWidth, height: columns ? rowHeight : badgeHeight, fill: "#eeedf8", radius: 17 });
      drawLines(badge, cardInset + 12, y + 6, FONT, primary);
      drawLines(identity, columns ? cardInset + badgeWidth + gap : cardInset, columns ? y + 6 : y + badgeHeight + 12, FONT, foreground);
      y += rowHeight + 26;
    }
  }
  y += 9;
  cards.unshift({ x: inset, y: cardTop, width: contentWidth, height: y - cardTop, fill: "#ffffff", border: primary, radius: 22 });
  y += 35;
  y += paragraph(`참가자 ${model.participantCount}명 · 당첨자 ${model.prizes.reduce((count, prize) => count + prize.winners.length, 0)}명`, y, HEADING_FONT);
  if (model.scheduledAt !== null) y += paragraph(`예정: ${stamp(model.scheduledAt)} (한국 시간)`, y, FONT, muted);
  y += paragraph(`실행: ${stamp(model.executedAt)} (한국 시간)${model.delayed ? " · 지연 실행" : ""}`, y, FONT, muted);
  y += paragraph(`원본: ${model.articleUrl}`, y, FONT, muted);
  const height = y + inset;
  if (height > 8192 || width * height > 16000000) throw invalid("dimensions");
  return { text, cards, height };
}
export function makeResultImage(host: ResultCanvasHost): typeof ResultImage.Service {
  return { renderPng: (input) => Effect.gen(function*() {
    const model = yield* Effect.try({ try: () => validate(input), catch: () => invalid("invalid-model") });
    yield* Effect.tryPromise({ try: () => host.fontsReady(), catch: () => invalid("font") }).pipe(Effect.timeoutOrElse({
      duration: "5 seconds", orElse: () => Effect.fail(invalid("font")),
    }));
    const canvas = yield* Effect.try({ try: () => host.createCanvas(), catch: () => invalid("encoding") });
    return yield* Effect.gen(function*() {
      const width = host.width ?? RESULT_CANVAS_WIDTH;
      if (!Number.isSafeInteger(width) || width < 256 || width > 2048) return yield* Effect.fail(invalid("dimensions"));
      const layout = yield* Effect.try({ try: () => {
        const context = canvas.getContext("2d");
        if (context === null) throw invalid("encoding");
        canvas.width = width; canvas.height = 1;
        return { ...layoutResult(context, model, width), context };
      }, catch: (error) => error instanceof ResultImageError ? error : invalid("encoding") });
      yield* Effect.try({ try: () => {
        canvas.height = layout.height;
        const context = layout.context;
        context.fillStyle = "#ffffff"; context.fillRect(0, 0, width, layout.height);
        for (const card of layout.cards) {
          context.beginPath(); context.roundRect(card.x, card.y, card.width, card.height, card.radius);
          context.fillStyle = card.fill; context.fill();
          if (card.border !== undefined) { context.strokeStyle = card.border; context.lineWidth = 2.5; context.stroke(); }
        }
        context.textBaseline = "top";
        for (const line of layout.text) { context.font = line.font; context.fillStyle = line.color; context.fillText(line.text, line.x, line.y); }
      }, catch: () => invalid("encoding") });
      return yield* Effect.callback<Blob, ResultImageError>((resume) => {
        let active = true;
        try {
          canvas.toBlob((blob) => {
            if (!active) return;
            active = false;
            if (blob === null || blob.type !== "image/png" || blob.size === 0) resume(Effect.fail(invalid("encoding")));
            else if (blob.size > RESULT_PNG_LIMIT) resume(Effect.fail(invalid("size")));
            else resume(Effect.succeed(blob));
          }, "image/png");
        } catch { active = false; resume(Effect.fail(invalid("encoding"))); }
        return Effect.sync(() => { active = false; });
      }).pipe(Effect.timeoutOrElse({ duration: "15 seconds", orElse: () => Effect.fail(invalid("encoding")) }));
    }).pipe(Effect.ensuring(Effect.sync(() => { canvas.width = 0; canvas.height = 0; })));
  }) };
}
export function resultImageForDocument(doc: Document): typeof ResultImage.Service {
  return makeResultImage({
    createCanvas: () => doc.createElement("canvas"),
    fontsReady: () => doc.fonts.ready.then(() => Promise.all([FONT, HEADING_FONT, TITLE_FONT].map((font) => doc.fonts.load(font, "가나다😀")))),
  });
}
