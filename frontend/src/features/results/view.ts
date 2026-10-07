import { Clock, Effect, Exit, Fiber, Scope, Semaphore } from "effect";
import type { Route } from "../../app/shellState";
import type { Collection, CollectionCommandRequest, CollectionReadOptions, Round } from "../../contracts/product";
import { ProtocolError } from "../../contracts/backend";
import type { ProductCommands } from "../../operations/productCoordinator";
import type { ResultImage } from "../../platform/canvas";
import { DomPlatform } from "../../platform/dom";
import { mountDialog } from "../../ui/dialog/view";
import { connectField, patchFieldError } from "../../ui/fields/view";
import { mountView, patchText } from "../../ui/view";
import { mountExportProduct, type ProductDispatch, type ResultExportPorts, type ResultExportFailure } from "../export/view";
import { productErrorMessage } from "../../app/productErrors";
import { mountFrozenParticipants } from "./frozen/view";
import { badgeCategoryLabels, badgePolicyText, participantCategory } from "../../app/participantCategories";

export interface ResultProductPorts {
  readonly renderer?: typeof ResultImage.Service;
  readonly export?: ResultExportPorts;
  readonly openArticle?: (url: string) => Effect.Effect<void, ResultExportFailure>;
}
export function kstSchedule(now: number, days: number, period: "am" | "pm", hour: number, minute: number): string {
  if (!Number.isFinite(now) || !Number.isInteger(days) || days < 0 || days > 3 ||
    !Number.isInteger(hour) || hour < 0 || hour > 11 || !Number.isInteger(minute) || minute < 0 || minute > 59 ||
    period !== "am" && period !== "pm") throw new Error("InvalidSchedule");
  const today = new Date(now + 9 * 3600000);
  const result = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() + days, hour + (period === "pm" ? 12 : 0), minute, 0) - 9 * 3600000;
  if (!Number.isFinite(result) || result <= now) throw new Error("InvalidSchedule");
  return new Date(result).toISOString();
}
function stateText(round: Round): string {
  switch (round.state) {
    case "pending_schedule": return "예약 대기";
    case "scheduled": return "예약됨";
    case "executing": return "추첨 중";
    case "completed": return "완료";
    case "cancelled": return "취소됨";
    case "failed": return "실패 · 저장된 회차를 확인한 뒤 재시도";
  }
}
function koreanTime(value: string | null): string {
  return value === null ? "미정" : new Intl.DateTimeFormat("ko-KR", { timeZone: "Asia/Seoul", dateStyle: "medium", timeStyle: "medium" }).format(Date.parse(value));
}
type Command = Omit<CollectionCommandRequest, "operationId" | "protocolVersion" | "backendSessionId"> & { readonly quickDelaySeconds: number | null };
export function mountResultProduct(host: HTMLElement, commands: ProductCommands, route: Extract<Route, { readonly _tag: "result" }>, dispatch: ProductDispatch, ports: ResultProductPorts = {}) {
  return mountView(host, Effect.gen(function*() {
    const scope = yield* Scope.Scope; const dom = yield* DomPlatform; const doc = host.ownerDocument;
    const element = doc.createElement("section"); element.className = "result-product"; element.dataset.productScreen = "result";
    const heading = doc.createElement("h2"); heading.textContent = "추첨 결과";
    const summary = doc.createElement("div"); summary.className = "result-header app-card";
    const title = doc.createElement("h3");
    const article = doc.createElement("p"); article.className = "result-meta"; const numbers = doc.createElement("p"); numbers.className = "result-summary"; const filters = doc.createElement("p"); filters.className = "result-filters";
    const roundControls = doc.createElement("div"); roundControls.className = "result-round-controls app-card";
    const roundLabel = doc.createElement("label"); roundLabel.className = "ui-field"; roundLabel.textContent = "회차 ";
    const roundSelect = doc.createElement("select"); roundSelect.name = "resultRound"; roundLabel.append(roundSelect);
    const roundPage = doc.createElement("p"); roundPage.setAttribute("role", "status");
    const newer = doc.createElement("button"); newer.type = "button"; newer.textContent = "더 최근 회차";
    const older = doc.createElement("button"); older.type = "button"; older.textContent = "이전 회차 보기";
    const roundNavigation = doc.createElement("nav"); roundNavigation.className = "ui-actions"; roundNavigation.setAttribute("aria-label", "회차 페이지"); roundNavigation.append(newer, older);
    const resultBody = doc.createElement("div"); resultBody.className = "result-body app-card";
    const details = doc.createElement("div"); details.className = "result-details"; const winners = doc.createElement("ol"); winners.className = "result-winners"; winners.setAttribute("aria-label", "품목별 당첨자");
    const status = doc.createElement("p"); status.setAttribute("role", "status");
    const error = doc.createElement("p"); error.setAttribute("role", "alert"); error.hidden = true;
    const actions = doc.createElement("div"); actions.className = "result-actions ui-actions";
    const refresh = doc.createElement("button"); refresh.className = "ui-button-ghost"; refresh.type = "button"; refresh.textContent = "다시 조회";
    const openArticle = doc.createElement("button"); openArticle.type = "button"; openArticle.textContent = "원본글 열기"; openArticle.disabled = ports.openArticle === undefined;
    const frozen = doc.createElement("button"); frozen.type = "button"; frozen.textContent = "확정 참가자 보기";
    const rerun = doc.createElement("button"); rerun.className = "ui-button-primary"; rerun.type = "button"; rerun.textContent = "재추첨";
    const cancel = doc.createElement("button"); cancel.className = "ui-button-danger"; cancel.type = "button"; cancel.textContent = "예약 취소";
    const retry = doc.createElement("button"); retry.className = "ui-button-primary"; retry.type = "button"; retry.textContent = "실패 회차 재시도";
    const schedule = doc.createElement("section"); schedule.className = "result-schedule app-card"; schedule.setAttribute("aria-label", "예약 시각 설정");
    const scheduleHeading = doc.createElement("h3"); scheduleHeading.textContent = "예약 시각";
    const quick = doc.createElement("div"); quick.className = "result-quick-schedule ui-actions";
    const quickButtons: HTMLButtonElement[] = [];
    const direct = doc.createElement("form"); direct.className = "result-schedule-form";
    const selectField = (name: string, text: string, options: ReadonlyArray<readonly [string, string]>) => {
      const label = doc.createElement("label"); label.className = "ui-field"; label.textContent = text; const input = doc.createElement("select"); input.name = name;
      for (const [value, caption] of options) { const option = doc.createElement("option"); option.value = value; option.textContent = caption; input.append(option); }
      label.append(input); direct.append(label); return input;
    };
    const days = selectField("scheduleDays", "날짜 ", [0,1,2,3].map((value) => [String(value), value === 0 ? "오늘" : `${value}일 후`] as const));
    const period = selectField("schedulePeriod", "오전·오후 ", [["am","오전"],["pm","오후"]]);
    const hour = selectField("scheduleHour", "시 ", Array.from({ length: 12 }, (_, value) => [String(value), `${value === 0 ? 12 : value}시`] as const));
    const minuteLabel = doc.createElement("label"); minuteLabel.className = "ui-field"; minuteLabel.textContent = "분 ";
    const minute = doc.createElement("input"); minute.name = "scheduleMinute"; minute.type = "number"; minute.min = "0"; minute.max = "59"; minute.step = "1"; minute.value = "0"; minute.required = true; minuteLabel.append(minute);
    const directButton = doc.createElement("button"); directButton.className = "ui-button-primary"; directButton.type = "submit"; directButton.textContent = "직접 예약";
    const scheduleError = doc.createElement("p"); scheduleError.id = "result-schedule-error"; scheduleError.hidden = true; scheduleError.setAttribute("role","alert");
    minute.id = "result-schedule-minute"; connectField(minuteLabel, minute, scheduleError);
    const policy = doc.createElement("p"); policy.textContent = "한국 시간 · 선택한 분의 00초에 실행합니다. 실행 10초 전부터 예약 취소·변경을 할 수 없습니다. 앱 종료·절전 중에는 실행하지 않고 다음 시작·복귀 시 지연 실행합니다.";
    direct.append(minuteLabel,directButton,scheduleError); schedule.append(scheduleHeading,quick,direct,policy);
    const exitPolicy = doc.createElement("p"); exitPolicy.className = "result-exit-policy"; exitPolicy.setAttribute("role","status");
    const exports = doc.createElement("div"); exports.className = "result-export";
    summary.append(title, article, numbers, filters);
    roundControls.append(roundLabel, roundPage, roundNavigation);
    resultBody.append(details, winners, status, error);
    actions.append(refresh, openArticle, frozen, rerun, cancel, retry);
    element.append(heading, summary, roundControls, resultBody, actions, schedule, exitPolicy, exports);
    let alive = true; let queryPending = false; let commandPending = false; let dialogOpen = false; let healthy = false;
    let patchExport=()=>{}; let collection: Collection | null = null; let selectedId: string | null = route.roundId;
    let backendTime = 0; let localTime = 0; let estimatedNow = 0;
    yield* Effect.addFinalizer(() => Effect.sync(() => { alive = false; collection = null; }));
    const latest = () => collection?.latestRound ?? null;
    const selected = () => collection?.rounds.find((round) => round.roundId === selectedId) ?? null;
    const patch = () => {
      if (!alive) return;
      patchText(exitPolicy,"앱 종료·절전 중에는 실행되지 않으며 다음 시작·복귀 시 만료 예약을 지연 실행합니다.");
      const last = latest(); const round = selected();
      const admin = healthy && last !== null && round?.roundId === last.roundId;
      const blocked = queryPending || commandPending || dialogOpen;
      refresh.disabled = queryPending || commandPending;
      frozen.disabled = collection === null || blocked;
      rerun.disabled = !admin || blocked || collection === null || collection.remainingCount === 0 || last?.state !== "completed" && last?.state !== "cancelled";
      cancel.hidden = last?.state !== "pending_schedule" && last?.state !== "scheduled";
      cancel.disabled = !admin || blocked || last?.state === "scheduled" && (last.scheduledAt === null || estimatedNow >= Date.parse(last.scheduledAt) - 10000);
      retry.hidden = last?.state !== "failed"; retry.disabled = !admin || blocked || last?.state !== "failed";
      schedule.hidden = last?.state !== "pending_schedule";
      for (const button of quickButtons) button.disabled = !admin || blocked || last?.state !== "pending_schedule";
      directButton.disabled = !admin || blocked || last?.state !== "pending_schedule";
      roundSelect.disabled = collection === null || queryPending || commandPending;
      newer.disabled = blocked || collection === null || collection.roundOffset === 0;
      older.disabled = blocked || collection === null || collection.roundOffset + 50 >= collection.roundTotal;
      patchText(roundPage, collection === null ? "회차를 조회하고 있습니다." : collection.rounds.length === 0 ? `전체 ${collection.roundTotal}회차 · 이 페이지에 회차가 없습니다.` : `전체 ${collection.roundTotal}회차 · ${collection.rounds[0]!.number}~${collection.rounds.at(-1)!.number}회차`);
      if (collection === null || round === null) {if(round===null){details.replaceChildren();winners.replaceChildren();}patchExport();return;}
      patchText(title, collection.article.title);
      patchText(article, `${collection.article.galleryName} · 묶음 ${collection.collectionId} · 작성자 ${collection.article.authorNickname||"확인 불가"} · 게시글 ${koreanTime(collection.article.postedAt)} · 원본 ${collection.article.url}`);
      patchText(numbers, `수집 참가자 ${collection.participantCount}명 · 확정 ${collection.selectedCount}명 · 남은 참가자 ${collection.remainingCount}명`);
      const f = collection.filters;
      patchText(filters, `확정 조건: ${f.excludeAnonymous ? "유동 제외 · " : ""}${f.excludeAuthor ? (collection.article.authorIdentifier===null?"작성자 판별 불가 · ":"작성자 제외 · ") : ""}${f.excludeDcconOnly ? "디시콘 전용 제외 · " : ""}포함 키워드 [${f.includeKeywords.join(", ")}] · 제외 키워드 [${f.excludeKeywords.join(", ")}]${f.timeCut === null ? "" : " · 시간컷 "+koreanTime(new Date(Date.parse(f.timeCut)-60000).toISOString())+"까지 (한국 시간)"} · ${badgePolicyText(f)}`);
      if (roundSelect.value !== round.roundId) roundSelect.value = round.roundId;
      const text = [`${round.number}회차 · ${stateText(round)}`, `예정: ${koreanTime(round.scheduledAt)}`, `실행: ${koreanTime(round.executedAt)}`, `관리 메시지: ${round.message || "없음"}`];
      if (round.scheduledAt !== null && round.executedAt !== null && Date.parse(round.executedAt) > Date.parse(round.scheduledAt) + 1000) text.push("지연 실행");
      if (round.state === "scheduled" && round.scheduledAt !== null) text.push(`남은 시간: ${Math.max(0, Math.ceil((Date.parse(round.scheduledAt)-estimatedNow)/1000))}초 · Go의 저장된 완료 결과를 확인합니다.`);
      details.replaceChildren(...text.map((value) => { const p=doc.createElement("p"); p.textContent=value; return p; }));
      winners.replaceChildren();
      if (round.state === "completed") {
        for (const [prizeIndex,prize] of round.prizes.entries()) {
          const item=doc.createElement("li"); const name=doc.createElement("strong"); name.textContent=prize.name||(round.prizes.length===1?"기본 상품":"상품 "+String.fromCharCode(65+prizeIndex));
          const list=doc.createElement("ul");
          for (const winner of round.winners.filter((value)=>value.prizeId===prize.id)) { const person=doc.createElement("li"); person.textContent=`${winner.participant.nickname} (${winner.participant.publicIdentifier})`; const badge=doc.createElement("span");badge.className="participant-badge";const category=participantCategory(winner.participant);badge.dataset.category=category;badge.textContent=badgeCategoryLabels[category];person.append(" ",badge);list.append(person); }
          item.append(name,list); winners.append(item);
        }
      }
      if (!healthy) patchText(status,"마지막 확인된 기록입니다. 다시 조회한 뒤 관리 명령을 사용해 주세요.");
      else if (last?.roundId !== round.roundId) patchText(status,"지난 회차를 읽고 있습니다. 관리 명령은 최신 회차에서 사용해 주세요.");
      else patchText(status,"저장된 기록을 확인했습니다. 최신 회차에서 관리 동작을 사용할 수 있습니다.");patchExport();
    };
    const apply = (value:Collection, chooseLatest:boolean) => {
      collection=value;
      if(chooseLatest || selectedId===null) selectedId=value.rounds.at(-1)?.roundId??null;
      roundSelect.replaceChildren(...value.rounds.map((round)=>{const option=doc.createElement("option");option.value=round.roundId;option.textContent=`${round.number}회차 · ${stateText(round)}`;return option;}));
      if (selected()===null) { healthy=false;patchText(error,"요청한 회차를 찾을 수 없습니다.");error.hidden=false; }
    };
    const load=(options:CollectionReadOptions=selectedId===null?{roundOffset:collection?.roundOffset??0}:{roundId:selectedId},choosePage=false)=>Effect.gen(function*(){
      if(!alive||queryPending||commandPending)return;queryPending=true;patch();
      const reply=yield* commands.getCollection(route.collectionId,options);
      const now=yield* Clock.currentTimeMillis;
      if(!alive)return;
      if(reply.data.collectionId!==route.collectionId || collection!==null&&reply.data.revision<collection.revision) return yield* Effect.fail(new ProtocolError());
      backendTime=Date.parse(reply.occurredAt);localTime=now;estimatedNow=backendTime;healthy=true;error.hidden=true;
      apply(reply.data,choosePage);patch();
    }).pipe(Effect.catch((failure)=>Effect.sync(()=>{if(alive){healthy=false;patchText(error,productErrorMessage(failure));error.hidden=false;}})),Effect.ensuring(Effect.sync(()=>{queryPending=false;if(alive)patch();})));
    const send=(effect:Effect.Effect<void,never,DomPlatform|Scope.Scope>)=>dispatch(effect.pipe(Scope.provide(scope),Effect.forkIn(scope),Effect.flatMap(Fiber.join),Effect.ignore));
    const request = (round:Round, extra:Partial<Command>={}):Command => ({
      collectionId:route.collectionId,roundId:round.roundId,expectedRevision:collection?.revision??0,expectedVersion:round.roundVersion,
      prizes:[],message:"",mode:"reservation",scheduledAt:null,quickDelaySeconds:null,...extra,
    });
    const command=(kind:"setSchedule"|"cancelSchedule"|"retryRound"|"rerun", payload:Command)=>Effect.gen(function*(){
      if(!alive||commandPending||!healthy)return;
      commandPending=true;patch();error.hidden=true;
      const updated=yield* commands.collectionCommand(kind,payload);
      if(alive){healthy=true;apply(updated,true);patch();}
    }).pipe(Effect.catch((failure)=>Effect.sync(()=>{if(alive){healthy=false;patchText(error,productErrorMessage(failure));error.hidden=false;}})),Effect.ensuring(Effect.sync(()=>{commandPending=false;if(alive)patch();})));
    for(const [seconds,caption] of [[10,"10초 후"],[30,"30초 후"],[60,"1분 후"],[120,"2분 후"]] as const){
      const button=doc.createElement("button");button.type="button";button.textContent=caption;quick.append(button);quickButtons.push(button);
      yield* dom.listen(button,"click",()=>{const round=latest();if(round!==null&&!button.disabled)send(command("setSchedule",request(round,{quickDelaySeconds:seconds})));});
    }
    yield* dom.listen(direct,"submit",(event)=>{
      event.preventDefault();const round=latest();if(round===null||directButton.disabled)return;
      try{
        if(period.value!=="am"&&period.value!=="pm"||! /^[0-3]$/.test(days.value)||! /^(?:[0-9]|1[01])$/.test(hour.value)||! /^(?:[0-9]|[1-5][0-9])$/.test(minute.value))throw new Error("InvalidSchedule");
        const at=kstSchedule(estimatedNow,Number(days.value),period.value,Number(hour.value),Number(minute.value));
        patchFieldError(minute,scheduleError,null);send(command("setSchedule",request(round,{scheduledAt:at})));
      }catch{patchFieldError(minute,scheduleError,"현재보다 이후인 한국 날짜·시·분을 선택해 주세요.");}
    });
    yield* dom.listen(refresh,"click",()=>{if(!refresh.disabled)send(load());});
    yield* dom.listen(newer,"click",()=>{if(!newer.disabled&&collection!==null)send(load({roundOffset:Math.max(0,collection.roundOffset-50)},true));});
    yield* dom.listen(older,"click",()=>{if(!older.disabled&&collection!==null)send(load({roundOffset:collection.roundOffset+50},true));});
    yield* dom.listen(roundSelect,"change",()=>{selectedId=roundSelect.value;patch();});
    yield* dom.listen(cancel,"click",()=>{const round=latest();if(round!==null&&!cancel.disabled)send(command("cancelSchedule",request(round)));});
    yield* dom.listen(retry,"click",()=>{const round=latest();if(round!==null&&!retry.disabled)send(command("retryRound",request(round)));});
    yield* dom.listen(openArticle,"click",()=>{if(collection!==null&&ports.openArticle!==undefined)send(ports.openArticle(collection.article.url).pipe(Effect.catch(()=>Effect.sync(()=>{if(alive){patchText(error,"원본글을 열 수 없습니다.");error.hidden=false;}}))));});
    const rerunDialog=Effect.gen(function*(){
      const current=latest();if(!alive||dialogOpen||collection===null||current===null||rerun.disabled)return;
      const remaining=collection.remainingCount;dialogOpen=true;patch();
      const dialog=doc.createElement("dialog");const h=doc.createElement("h2");h.id="rerun-result-title";h.textContent="재추첨 설정";
      const form=doc.createElement("form");form.className="dialog-form";const modeLabel=doc.createElement("label");modeLabel.className="ui-field";modeLabel.textContent="품목 방식 ";
      const prizeMode=doc.createElement("select");prizeMode.name="prizeMode";for(const [value,text]of[["single","단일 품목"],["multiple","여러 품목"]]as const){const option=doc.createElement("option");option.value=value;option.textContent=text;prizeMode.append(option);}modeLabel.append(prizeMode);
      const rows=doc.createElement("div");const singleRows=doc.createElement("div");const manyRows=doc.createElement("div");manyRows.hidden=true;rows.append(singleRows,manyRows);
      const entries:Array<{name:HTMLInputElement;count:HTMLInputElement;row:HTMLElement}>=[];
      let nextPrize=0;
      const makeRow=(single:boolean)=>{
        const index=single?1:++nextPrize;const row=doc.createElement("div");row.className="prize-row dialog-prize-row";const nameLabel=doc.createElement("label");nameLabel.className="ui-field";nameLabel.textContent=single?"단일 품목 ":"품목 ";
        const name=doc.createElement("input");name.name=single?"prizeName1":`manyPrizeName${index}`;name.maxLength=20;nameLabel.append(name);
        const countLabel=doc.createElement("label");countLabel.className="ui-field";countLabel.textContent="인원 ";const count=doc.createElement("input");count.name=single?"singleCount":`manyPrizeCount${index}`;count.type="number";count.min="1";count.max=String(Math.min(10,remaining));count.step="1";count.value="1";count.required=true;countLabel.append(count);
        row.append(nameLabel,countLabel);
        if(!single){const remove=doc.createElement("button");remove.className="ui-button-danger";remove.type="button";remove.textContent="품목 삭제";remove.dataset.deletePrize="true";row.append(remove);}
        (single?singleRows:manyRows).append(row);return{name,count,row};
      };
      const singleEntry=makeRow(true);entries.push(makeRow(false));
      const decrease=doc.createElement("button");decrease.type="button";decrease.textContent="인원 줄이기";
      const increase=doc.createElement("button");increase.type="button";increase.textContent="인원 늘리기";
      const countActions=doc.createElement("div");countActions.className="ui-actions";countActions.append(decrease,increase);singleEntry.row.append(countActions);
      const addRow=()=>{const total=entries.reduce((sum,item)=>sum+Number(item.count.value),0);if(entries.length>=10||total>=Math.min(10,remaining))return;entries.push(makeRow(false));};
      const add=doc.createElement("button");add.type="button";add.textContent="품목 추가";add.hidden=true;
      const modeLabel2=doc.createElement("label");modeLabel2.className="ui-field";modeLabel2.textContent="실행 방식 ";const drawMode=doc.createElement("select");drawMode.name="drawMode";
      for(const[value,text]of[["immediate","즉시 추첨"],["reservation","예약 대기 생성"]]as const){if(current.state==="completed"&&value==="reservation")continue;const option=doc.createElement("option");option.value=value;option.textContent=text;drawMode.append(option);}modeLabel2.append(drawMode);
      const messageLabel=doc.createElement("label");messageLabel.className="ui-field";messageLabel.textContent="관리 메시지 ";const message=doc.createElement("input");message.name="message";message.maxLength=20;messageLabel.append(message);
      const formError=doc.createElement("p");formError.setAttribute("role","alert");formError.hidden=true;
      const submit=doc.createElement("button");submit.className="ui-button-primary";submit.type="submit";submit.textContent="새 회차 추첨";
      const dismiss=doc.createElement("button");dismiss.type="button";dismiss.textContent="취소";
      const dialogActions=doc.createElement("div");dialogActions.className="dialog-actions ui-actions";dialogActions.append(submit,dismiss);
      form.append(modeLabel,rows,add,modeLabel2,messageLabel,formError,dialogActions);dialog.append(h,form);
      const first=singleEntry;
      const session=yield* mountDialog(element,Effect.gen(function*(){
        const counts=()=>{const many=prizeMode.value==="multiple";singleEntry.name.disabled=singleEntry.count.disabled=many;for(const entry of entries)entry.name.disabled=entry.count.disabled=!many;const total=entries.reduce((sum,item)=>sum+Number(item.count.value),0);add.disabled=entries.length>=10||total>=Math.min(10,remaining)||entries.some((entry)=>!Number.isSafeInteger(Number(entry.count.value))||Number(entry.count.value)<1);for(const entry of entries){const remove=entry.row.querySelector<HTMLButtonElement>("[data-delete-prize]");if(remove!==null)remove.disabled=entries.length===1;}decrease.disabled=Number(singleEntry.count.value)<=1;increase.disabled=Number(singleEntry.count.value)>=Math.min(10,remaining);};
        yield* dom.listen(add,"click",()=>{addRow();counts();});
        yield* dom.listen(form,"input",counts);
        yield* dom.listen(decrease,"click",()=>{const value=Number(singleEntry.count.value);if(Number.isSafeInteger(value)&&value>1)singleEntry.count.value=String(value-1);counts();});
        yield* dom.listen(increase,"click",()=>{const value=Number(singleEntry.count.value);if(Number.isSafeInteger(value)&&value>=1&&value<Math.min(10,remaining))singleEntry.count.value=String(value+1);counts();});
        yield* dom.listen(manyRows,"click",(event)=>{const button=event.target instanceof Element?event.target.closest("[data-delete-prize]"):null;const index=entries.findIndex((entry)=>button!==null&&entry.row.contains(button));if(index>=0&&entries.length>1){entries[index]?.row.remove();entries.splice(index,1);counts();}});counts();
        yield* dom.listen(prizeMode,"change",()=>{add.hidden=prizeMode.value!=="multiple";singleRows.hidden=prizeMode.value==="multiple";manyRows.hidden=prizeMode.value!=="multiple";counts();});
        yield* dom.listen(dismiss,"click",()=>dialog.close());
        yield* dom.listen(form,"submit",(event)=>{event.preventDefault();if(submit.disabled)return;
          if(current.state==="completed"&&drawMode.value==="reservation"){patchText(formError,"완료된 회차의 재추첨은 즉시 실행만 가능합니다.");formError.hidden=false;return;}
          const chosen=prizeMode.value==="multiple"?entries:[singleEntry];
          const prizes=chosen.map((entry,index)=>({id:`rerun-prize-${index+1}`,name:entry.name.value,count:Number(entry.count.value)}));
          const sum=prizes.reduce((total,item)=>total+item.count,0);
          if(prizes.some((item)=>!Number.isSafeInteger(item.count)||item.count<1||item.count>10||item.name.length>20)||sum>10||sum>remaining||message.value.length>20||prizeMode.value!=="single"&&prizeMode.value!=="multiple"||drawMode.value!=="immediate"&&drawMode.value!=="reservation"){
            patchText(formError,"품목명20자·메시지20자와 인원1~10명·남은 참가자 수를 확인해 주세요.");formError.hidden=false;return;
          }
          submit.disabled=true;
          send(command("rerun",request(current,{prizes,message:message.value,mode:drawMode.value})).pipe(Effect.andThen(Effect.sync(()=>{if(healthy)dialog.close();else{patchText(formError,error.textContent??"요청을 처리하지 못했습니다.");formError.hidden=false;}})),Effect.ensuring(Effect.sync(()=>{submit.disabled=false;}))));
        });
        return{element:dialog,value:{title:h,initialFocus:first.name,sensitiveInputs:[],restoreFocus:rerun,beforeFocusRestore:Effect.sync(()=>{dialogOpen=false;if(alive)patch();})}};
      }));
      yield* session.closed;
    }).pipe(Effect.catch(()=>Effect.sync(()=>{if(alive){patchText(error,"재추첨 설정 창을 열 수 없습니다.");error.hidden=false;}})),Effect.ensuring(Effect.sync(()=>{dialogOpen=false;if(alive)patch();})));
    yield* dom.listen(rerun,"click",()=>{if(!rerun.disabled)send(rerunDialog);});
    const frozenDialog=Effect.gen(function*(){
      if(!alive||dialogOpen||collection===null||frozen.disabled)return;
      dialogOpen=true;patch();
      const dialog=doc.createElement("dialog");const h=doc.createElement("h2");h.id="frozen-participants-title";h.textContent="확정 참가자 보기";dialog.append(h);
      const session=yield* mountDialog(element,Effect.gen(function*(){
        const mounted=yield* mountFrozenParticipants(dialog,commands,route.collectionId,dispatch);
        const dismiss=doc.createElement("button");dismiss.type="button";dismiss.textContent="명단 닫기";
        const dialogActions=doc.createElement("div");dialogActions.className="dialog-actions ui-actions";dialogActions.append(dismiss);dialog.append(dialogActions);
        yield* dom.listen(dismiss,"click",()=>dialog.close());
        return{element:dialog,value:{title:h,initialFocus:mounted.value.initialFocus,sensitiveInputs:[],restoreFocus:frozen,beforeFocusRestore:Effect.sync(()=>{dialogOpen=false;if(alive)patch();})}};
      }));
      yield* session.closed;
    }).pipe(Effect.catch(()=>Effect.sync(()=>{if(alive){patchText(error,"확정 명단 창을 열 수 없습니다.");error.hidden=false;}})),Effect.ensuring(Effect.sync(()=>{dialogOpen=false;if(alive)patch();})));
    yield* dom.listen(frozen,"click",()=>{if(!frozen.disabled)send(frozenDialog);});
    const share=yield* mountExportProduct(exports,()=>{const round=selected();return collection===null||round===null?null:{collection,round};},dispatch,ports.export,ports.renderer);
    patchExport=share.value.patch;const patchAll=patch;
    yield* dom.listen(roundSelect,"change",patchAll);
    yield* load();patchAll();
    const tickerLock=yield* Semaphore.make(1);let tickerScope:Scope.Closeable|undefined;
    const synchronizeTicker=(requery:boolean)=>tickerLock.withPermit(Effect.gen(function*(){
      if(!alive)return;
      const visible=(yield* dom.visibility)==="visible";
      if(visible&&tickerScope!==undefined&&(!requery||queryPending))return;
      const previous=tickerScope;tickerScope=undefined;if(previous!==undefined)yield* Scope.close(previous,Exit.void);
      if(!visible||!alive)return;
      const child=yield* Scope.fork(scope,"sequential");tickerScope=child;
      yield* Effect.gen(function*(){
        if(requery){yield* load();}
        while(true){
          yield* Effect.sleep("1 second");
          const now=yield* Clock.currentTimeMillis;estimatedNow=backendTime+Math.max(0,now-localTime);
          if(collection?.latestRound.state==="scheduled"||collection?.latestRound.state==="executing"){yield* load();patchAll();}
        }
      }).pipe(Scope.provide(child),Effect.forkIn(child));
    })).pipe(Effect.catch(()=>Effect.sync(()=>{if(alive){healthy=false;patchText(error,"화면 연결 상태를 확인한 뒤 다시 조회해 주세요.");error.hidden=false;patch();}})));
    yield* Effect.addFinalizer(()=>Effect.sync(()=>{tickerScope=undefined;}));
    yield* dom.listen(doc,"visibilitychange",()=>send(synchronizeTicker(true)));
    if(doc.defaultView!==null)yield* dom.listen(doc.defaultView,"focus",()=>send(synchronizeTicker(true)));
    yield* synchronizeTicker(false);
    return {element,value:{refresh:Effect.suspend(()=>load())}};
  }));
}
