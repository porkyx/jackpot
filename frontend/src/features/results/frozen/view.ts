import { Effect, Fiber, Scope, Semaphore } from "effect";
import { ProtocolError } from "../../../contracts/backend";
import type { FrozenCommentsPage, FrozenParticipantsPage, FrozenCommentsQuery, FrozenParticipantsQuery, Participant, ProductReply } from "../../../contracts/product";
import type { ProductError } from "../../../operations/productCoordinator";
import { DomPlatform } from "../../../platform/dom";
import { mountView, patchText,makeKeyedRows } from "../../../ui/view";
import type { ProductDispatch } from "../../export/view";
import { productErrorMessage } from "../../../app/productErrors";
import {makeCommentRow} from "../../../ui/comment/view";
import {badgeCategoryLabels,participantCategory} from "../../../app/participantCategories";

export interface FrozenReadCommands {
  readonly queryFrozenParticipants: (query: FrozenParticipantsQuery) => Effect.Effect<ProductReply<typeof FrozenParticipantsPage.Type>, ProductError>;
  readonly queryFrozenComments: (query: FrozenCommentsQuery) => Effect.Effect<ProductReply<typeof FrozenCommentsPage.Type>, ProductError>;
}
const groups = [["", "전체"], ["unclassified", "미분류"], ["included", "포함"], ["excluded", "제외"]] as const;
const classification = (value: Participant["classification"]) => value === "included" ? "자동 포함" : value === "excluded" ? "자동 제외" : "미분류";
const time = (value: string | null) => value === null ? "시각 확인 불가" : new Intl.DateTimeFormat("ko-KR", { timeZone: "Asia/Seoul", dateStyle: "medium", timeStyle: "medium" }).format(Date.parse(value));

// This queries the persisted snapshot. It exposes no draft edits, selection
// toggles, passwords, or mutation commands.
export function mountFrozenParticipants(host: HTMLElement, reads: FrozenReadCommands, collectionId: string, dispatch: ProductDispatch) {
  return mountView(host, Effect.gen(function*() {
    const scope = yield* Scope.Scope; const dom = yield* DomPlatform; const doc = host.ownerDocument;
    const element = doc.createElement("section"); element.className = "frozen-participants"; element.dataset.frozenParticipants = "true";
    const explanation = doc.createElement("p"); explanation.textContent = "추첨을 확정할 때 저장된 참가자·댓글입니다. 조건과 선택 상태는 변경할 수 없습니다.";
    const form = doc.createElement("form"); form.className = "frozen-toolbar";
    const queryLabel = doc.createElement("label"); queryLabel.className = "ui-field"; queryLabel.textContent = "닉네임·식별자 검색 ";
    const query = doc.createElement("input"); query.name = "frozenQuery"; query.type = "search"; query.maxLength = 200; queryLabel.append(query);
    const groupLabel = doc.createElement("label"); groupLabel.className = "ui-field"; groupLabel.textContent = "자동 분류 ";
    const group = doc.createElement("select"); group.name = "frozenGroup";
    for (const [value, caption] of groups) { const option = doc.createElement("option"); option.value = value; option.textContent = caption; group.append(option); }
    groupLabel.append(group);
    const search = doc.createElement("button"); search.className = "ui-button-primary"; search.type = "submit"; search.textContent = "명단 검색";
    const refresh = doc.createElement("button"); refresh.type = "button"; refresh.textContent = "명단 다시 조회";
    form.append(queryLabel, groupLabel, search, refresh);
    const status = doc.createElement("p"); status.setAttribute("role", "status");
    const error = doc.createElement("p"); error.setAttribute("role", "alert"); error.hidden = true;
    const rows = doc.createElement("ol"); rows.className = "frozen-list"; rows.setAttribute("aria-label", "확정 참가자");
    const previous = doc.createElement("button"); previous.type = "button"; previous.textContent = "이전 참가자 페이지";
    const next = doc.createElement("button"); next.type = "button"; next.textContent = "다음 참가자 페이지";
    const pagination = doc.createElement("div"); pagination.className = "frozen-pagination ui-actions"; pagination.append(previous, next);
    const comments = doc.createElement("section"); comments.className = "frozen-comments"; comments.setAttribute("aria-label", "확정 참가자의 댓글"); comments.hidden = true;
    const commentTitle = doc.createElement("h3"); const commentStatus = doc.createElement("p");
    const commentRows = doc.createElement("ol"); commentRows.setAttribute("aria-label", "저장된 댓글");
    const commentPrevious = doc.createElement("button"); commentPrevious.type = "button"; commentPrevious.textContent = "이전 댓글 페이지";
    const commentNext = doc.createElement("button"); commentNext.type = "button"; commentNext.textContent = "다음 댓글 페이지";
    const commentPagination = doc.createElement("div"); commentPagination.className = "frozen-pagination ui-actions"; commentPagination.append(commentPrevious, commentNext);
    comments.append(commentTitle, commentStatus, commentRows, commentPagination);
    const scopedComments=yield*makeKeyedRows(commentRows,50,(row:typeof FrozenCommentsPage.Type.rows[number])=>row.id,row=>makeCommentRow(row,doc,value=>(value.kind==="dccon"?"디시콘":value.kind==="voice"?"보이스 댓글":"텍스트 댓글")+" · "+time(value.postedAt)+(value.parentId===null?"":" · 답글("+value.parentId+")"),"li"));
    element.append(explanation, form, status, error, rows, pagination, comments);
    let alive = true; let pending = false; let session: string | null = null;
    let confirmed: typeof FrozenParticipantsPage.Type | null = null;
    let commentPage: typeof FrozenCommentsPage.Type | null = null;
    let participant: Participant | null = null; let appliedQuery = ""; let appliedGroup = "";
    let generation=0;let inputVersion=0;let active:Fiber.Fiber<void>|undefined;let activeIntent:string|null=null;const readLock=yield*Semaphore.make(1);
    yield* Effect.addFinalizer(() => Effect.sync(() => { alive = false; confirmed = null; participant = null; commentPage = null; }));
    const controls = () => {
      search.disabled = !alive;refresh.disabled = pending;
      previous.disabled = pending || confirmed === null || confirmed.offset === 0;
      next.disabled = pending || confirmed === null || confirmed.offset + confirmed.rows.length >= confirmed.matched;
      commentPrevious.disabled = pending || commentPage === null || commentPage.offset === 0;
      commentNext.disabled = pending || commentPage === null || commentPage.offset + commentPage.rows.length >= commentPage.total;
      for (const button of rows.querySelectorAll<HTMLButtonElement>("button")) button.disabled = pending;
    };
    const checkHeader = (reply: ProductReply<unknown>, revision: number, expected: number | null) => {
      if (reply.protocolVersion !== 1 || session !== null && reply.backendSessionId !== session ||
        expected !== null && revision !== expected || confirmed !== null && revision < confirmed.revision) throw new ProtocolError();
    };
    const failure = (cause: ProductError) => Effect.sync(() => {
      if (alive) { patchText(error, productErrorMessage(cause)); error.hidden = false; }
    });
    const run=(effect:(current:()=>boolean)=>Effect.Effect<void,ProductError>,intent:string,replace:boolean,fence:()=>boolean=()=>true)=>{
      if(!alive||pending&&(!replace||activeIntent===intent))return Effect.void;
      const requested=++generation;activeIntent=intent;
      const current=()=>alive&&generation===requested&&fence();
      return Effect.gen(function*(){
        const fiber=yield*readLock.withPermit(Effect.gen(function*(){
          if(!alive||generation!==requested)return;
          if(active!==undefined)yield*Fiber.interrupt(active);
          if(!current()){pending=false;activeIntent=null;if(alive)controls();return;}
          pending=true;controls();error.hidden=true;
          const next=yield*effect(current).pipe(Effect.catch((cause)=>current()?failure(cause):Effect.void),Effect.ensuring(Effect.sync(()=>{if(generation===requested){pending=false;activeIntent=null;if(alive)controls();}})),Effect.forkIn(scope));
          active=next;return next;
        }));
        if(fiber!==undefined)yield*Fiber.join(fiber);
      });
    };
    const load = (offset: number, text: string, filter: string, fresh: boolean) => {const requestedInput=inputVersion;const requestedQuery=query.value;const requestedGroup=group.value;return run((current)=>Effect.gen(function*() {
      if (text.length > 200 || !groups.some(([value]) => value === filter)) return yield* Effect.fail(new ProtocolError());
      const expected = fresh ? null : confirmed?.revision ?? null;
      const reply = yield* reads.queryFrozenParticipants({ collectionId, expectedRevision: expected, query: text, group: filter, offset, limit: 100 });
      if (!current()) return;
      const page = reply.data;
      yield* Effect.try({ try: () => {
        checkHeader(reply, page.revision, expected);
        if (page.collectionId !== collectionId || page.offset !== offset || page.matched > page.total || page.rows.length > 100 ||
          page.rows.length > Math.max(0, page.matched - offset) || new Set(page.rows.map((row) => row.id)).size !== page.rows.length) throw new ProtocolError();
      }, catch: () => new ProtocolError() });
      if(!current())return;
      session = reply.backendSessionId; confirmed = page; appliedQuery = text; appliedGroup = filter;
      participant = null; commentPage = null; comments.hidden = true; yield*scopedComments.patch([]).pipe(Effect.provideService(DomPlatform,dom),Effect.mapError(()=>new ProtocolError()));
      rows.replaceChildren();
      for (const row of page.rows) {
        const item = doc.createElement("li"); const button = doc.createElement("button"); button.className = "ui-button-ghost"; button.type = "button"; button.dataset.participantId = row.id;
        button.textContent = row.nickname + " (" + row.publicIdentifier + ")";
        const metadata = doc.createElement("p"); metadata.textContent = classification(row.classification) + " · " + (row.included ? "확정 선택" : "선택 안 됨") + " · 댓글 " + row.commentCount + "개";
        const preview = doc.createElement("div"); preview.dataset.commentPreviews="true";
        for(const text of row.previews){const paragraph=doc.createElement("p");paragraph.textContent=text;preview.append(paragraph);}
        const badge=doc.createElement("span");badge.className="participant-badge";const category=participantCategory(row);badge.dataset.category=category;badge.textContent=badgeCategoryLabels[category];
        item.append(button, badge, metadata, preview); rows.append(item);
      }
      patchText(status, page.matched === 0 ? "조건에 맞는 참가자가 없습니다. 저장된 전체 " + page.total + "명" :
        "저장된 전체 " + page.total + "명 · 검색 " + page.matched + "명 · " + (offset + 1) + "~" + (offset + page.rows.length) + "명");
    }),JSON.stringify(["participants",offset,text,filter,fresh,fresh?requestedInput:null]),true,()=>!fresh||inputVersion===requestedInput&&query.value===requestedQuery&&group.value===requestedGroup);};
    const loadComments = (chosen: Participant, offset: number) => run((current)=>Effect.gen(function*() {
      if (confirmed === null) return;
      const revision = confirmed.revision;
      const reply = yield* reads.queryFrozenComments({ collectionId, expectedRevision: revision, participantId: chosen.id, offset, limit: 50 });
      if (!current()) return;
      const page = reply.data;
      yield* Effect.try({ try: () => {
        checkHeader(reply, page.revision, revision);
        if (page.collectionId !== collectionId || page.participantId !== chosen.id || page.offset !== offset ||
          page.rows.length > 50 || page.rows.length > Math.max(0, page.total - offset) ||
          new Set(page.rows.map((row) => row.id)).size !== page.rows.length) throw new ProtocolError();
      }, catch: () => new ProtocolError() });
      if(!current())return;
      yield*scopedComments.patch(page.rows).pipe(Effect.provideService(DomPlatform,dom),Effect.mapError(()=>new ProtocolError()));
      if(!current())return;
      participant = chosen; commentPage = page; comments.hidden = false;
      patchText(commentTitle, chosen.nickname + "의 저장된 댓글");
      patchText(commentStatus, page.total === 0 ? "저장된 댓글이 없습니다." : "댓글 " + page.total + "개 · " + (offset + 1) + "~" + (offset + page.rows.length) + "개");
    }),JSON.stringify(["comments",chosen.id,offset]),false);
    const send = (effect: Effect.Effect<void, never>) => dispatch(effect.pipe(Effect.forkIn(scope), Effect.flatMap(Fiber.join), Effect.ignore));
    yield* dom.listen(form, "submit", (event) => { event.preventDefault(); send(load(0, query.value, group.value, true)); });
    yield* dom.listen(refresh, "click", () => send(load(confirmed?.offset ?? 0, appliedQuery, appliedGroup, true)));
    yield* dom.listen(previous, "click", () => { if (confirmed !== null && !previous.disabled) send(load(Math.max(0, confirmed.offset - 100), appliedQuery, appliedGroup, false)); });
    yield* dom.listen(next, "click", () => { if (confirmed !== null && !next.disabled) send(load(confirmed.offset + 100, appliedQuery, appliedGroup, false)); });
    yield* dom.listen(rows, "click", (event) => {
      const button = event.target instanceof Element ? event.target.closest<HTMLButtonElement>("button[data-participant-id]") : null;
      const chosen = confirmed?.rows.find((row) => row.id === button?.dataset.participantId);
      if (chosen !== undefined && button !== null && !button.disabled) send(loadComments(chosen, 0));
    });
    yield* dom.listen(commentPrevious, "click", () => { if (participant !== null && commentPage !== null && !commentPrevious.disabled) send(loadComments(participant, Math.max(0, commentPage.offset - 50))); });
    yield* dom.listen(commentNext, "click", () => { if (participant !== null && commentPage !== null && !commentNext.disabled) send(loadComments(participant, commentPage.offset + 50)); });
    yield*dom.listen(query,"input",()=>{inputVersion++;});
    yield*dom.listen(group,"change",()=>{inputVersion++;send(load(0,query.value,group.value,true));});
    controls(); yield* load(0, "", "", true).pipe(Effect.forkIn(scope));
    return { element, value: { initialFocus: query } };
  }));
}
