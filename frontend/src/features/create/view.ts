import { Cause, Effect, Exit, Option, Scope, Stream } from "effect";
import type { Draft, Participant, ParticipantsPage, CommentsPage } from "../../contracts/product";
import type { ProductCommands } from "../../operations/productCoordinator";
import { DomPlatform } from "../../platform/dom";
import { mountView, makeKeyedRows, patchText, patchInputValue, patchDisabled, patchAria } from "../../ui/view";
import { makeFramePatcher } from "../../ui/frame";
import { connectField, patchFieldError } from "../../ui/fields/view";
import { mountDialog, type DialogSession } from "../../ui/dialog/view";
import { bindCreateTextInput, type CreateInputBinding } from "../../screens/create/view";
import { getEditorField, isEditorFieldLocked, badgeEditorFields, type FieldTarget, type EditorInput, type InputFence, type PrizeInput } from "../../screens/create/editorModel";
import { badgeCategoryLabels, participantCategory } from "../../app/participantCategories";
import type { CreateWorkspace } from "../../screens/create/productOwner";
import { mountCreateScreenModel, type CreateScreenOwner } from "../../screens/create/screenOwner";
import { screenAcceptsRequest, type DraftScreenContext, type ParticipantGroup, type ParticipantRequest, type ParticipantSearch } from "../../screens/create/screenModel";
import {makeCommentRow} from "../../ui/comment/view";
import "./product.css";

const context = (draft: Draft | null): DraftScreenContext | null => draft === null ? null : ({ backendSessionId: draft.summary.backendSessionId, draftId: draft.summary.draftId, revision: draft.summary.revision, articleGeneration: draft.summary.articleGeneration });
const pageKey = (value: DraftScreenContext & ParticipantSearch) => JSON.stringify([value.backendSessionId,value.draftId,value.revision,value.articleGeneration,value.group,value.search,value.offset]);
const reasons: Readonly<Record<Participant["reason"], string>> = { "": "", anonymous: "유동닉 제외", badge_category: "분류 제외", author: "글쓴이 제외", dccon: "디시콘만 작성", time_cut: "시간컷", exclude_keyword: "제외 단어", include_keyword: "포함 단어" };

export function mountCreateProduct(host: HTMLElement, commands: ProductCommands, workspace: CreateWorkspace, goResult: (id: string) => void) {
  return Effect.gen(function*() {
    const owner = yield* Scope.fork(yield* Scope.Scope, "sequential");
    const dom = yield* DomPlatform;
    return yield* mountView(host, Effect.gen(function*() {
      const doc = host.ownerDocument;
      const node = <K extends keyof HTMLElementTagNameMap>(tag: K, text = ""): HTMLElementTagNameMap[K] => { const element = doc.createElement(tag); if (text !== "") element.textContent = text; return element; };
      const root = node("section"); root.className = "create-product";
      const headingRow = node("div"); headingRow.className="create-heading"; root.append(headingRow); const heading = node("h1", "추첨 만들기"); headingRow.append(heading);
      const alert = node("p"); alert.setAttribute("role","alert"); headingRow.append(alert);
      const status = node("p"); status.setAttribute("role","status"); status.setAttribute("aria-live","polite"); headingRow.append(status);
      const act = <A,E>(effect: Effect.Effect<A,E,Scope.Scope | DomPlatform>) => {
        workspace.dispatch(effect.pipe(Scope.provide(owner),Effect.provideService(DomPlatform,dom),Effect.catchCause(workspace.report),Effect.asVoid));
      };
      const writingButtons: HTMLButtonElement[] = [];
      const button = (parent: HTMLElement, label: string, action: () => void, writing = false) => Effect.gen(function*() {
        const element = node("button",label); element.type="button"; parent.append(element);if(writing)writingButtons.push(element);
        yield* dom.listen(element,"click",() => { if (owner.state._tag !== "Closed" && !element.disabled && (!writing || !workspace.read().writesBlocked)) action(); });
        return element;
      });
      let serial = 0;
      const field = (parent: HTMLElement, title: string, type = "text", stableId?: string) => {
        const box=node("div"); box.className="create-field ui-field"; if(type==="checkbox")box.classList.add("ui-field-checkbox"); const label=node("label",title); const input=node("input"); input.type=type; input.id=stableId??"create-field-"+(++serial); const error=node("p"); error.id=input.id+"-error"; error.hidden=true; connectField(label,input,error); box.append(label,input,error); parent.append(box); return { box,input,error };
      };
      const write = <E,R>(effect: Effect.Effect<void,E,R>) => Effect.suspend(() => workspace.read().writesBlocked ? Effect.void : effect);
      const bindings: Array<{input:HTMLInputElement;target:FieldTarget;binding:CreateInputBinding}> = [];
      const bind = (input:HTMLInputElement,target:FieldTarget,toInput:(raw:string)=>EditorInput,submit?:Effect.Effect<void,unknown>) => Effect.gen(function*() {
        const read = () => { const editor=workspace.read().editor; return editor===null ? "" : String(getEditorField(editor,target).raw); };
        const candidate = ():InputFence|null => { const editor=workspace.read().editor; if(editor===null || workspace.read().writesBlocked || isEditorFieldLocked(editor,target))return null; const value=getEditorField(editor,target); return value.dirty && value.submitted?.inputVersion!==value.inputVersion ? {identity:editor.identity,editorEpoch:editor.editorEpoch,inputVersion:value.inputVersion}:null; };
        const binding=yield* bindCreateTextInput(input,{readRaw:read,writeRaw:(raw)=>workspace.input(toInput(raw)),candidate:target._tag==="url"?()=>null:candidate,onDebounced:(fence)=>write(workspace.commitField(target,fence)),onSubmit:write(submit??workspace.commitField(target))},workspace.dispatch,workspace.report);
        bindings.push({input,target,binding}); return binding;
      });
      const article=node("section"); article.className="create-article app-card"; article.append(node("h2","게시글")); root.append(article);
      const articleToolbar=node("div");articleToolbar.className="create-article-toolbar";article.append(articleToolbar);
      const url=field(articleToolbar,"디시인사이드 게시글 주소","url"); url.input.placeholder="https://gall.dcinside.com/board/view/…";
      yield* bind(url.input,{_tag:"url"},(raw)=>({_tag:"UrlChanged",raw}),workspace.load);
      const load=yield* button(articleToolbar,"게시글 불러오기",()=>act(workspace.load),true);load.className="ui-button-primary";
      const articleActions=node("div");articleActions.className="ui-actions create-article-actions";articleToolbar.append(articleActions);
      const cancel=yield* button(articleActions,"수집 취소",()=>act(workspace.cancelLoad));cancel.className="ui-button-ghost";
      const resetArticle=yield* button(articleActions,"게시글 초기화",()=>act(workspace.resetArticle),true);resetArticle.className="ui-button-danger";
      const articleTitle=node("p");articleTitle.className="create-article-title"; article.append(articleTitle);

      const filterAccordion=node("details");filterAccordion.className="create-filters app-card";filterAccordion.dataset.filterAccordion="";root.append(filterAccordion);
      const filterSummary=node("summary");filterSummary.className="create-filter-summary";const filterCaption=node("span","자동 분류 필터");const filterHint=node("span","선택 설정");filterHint.className="create-filter-hint";filterSummary.append(filterCaption,filterHint);filterAccordion.append(filterSummary);
      const filters=node("fieldset");filters.className="create-filter-body";const filterLegend=node("legend","자동 분류 필터");filterLegend.className="create-filter-legend";filters.append(filterLegend);filterAccordion.append(filters);
      const filterOptions=node("div");filterOptions.className="filter-options";filters.append(filterOptions);
      const checks:Array<{input:HTMLInputElement;error:HTMLElement;target:FieldTarget}>=[];
      for(const [name,label] of [["excludeAnonymous","유동닉 제외"],["excludeAuthor","글쓴이 제외"],["excludeDcconOnly","디시콘만 작성한 참가자 제외"],["timeCutEnabled","시간컷 사용"]] as const) {
        const value=field(filterOptions,label,"checkbox"); const target:FieldTarget={_tag:"filterToggle",field:name}; checks.push({input:value.input,error:value.error,target});
        yield* dom.listen(value.input,"change",()=>{workspace.input({_tag:"FilterToggleChanged",field:name,raw:value.input.checked});act(workspace.commitField(target));});
      }
      const categories=node("section");categories.className="badge-rule-section";filters.append(categories);categories.append(node("h3","참가자 분류"));
      const weighting=field(categories,"분류별 당첨 비율 사용","checkbox","create-category-weighting");const weightingTarget:FieldTarget={_tag:"filterToggle",field:"weightingEnabled"};checks.push({input:weighting.input,error:weighting.error,target:weightingTarget});
      yield* dom.listen(weighting.input,"change",()=>{workspace.input({_tag:"FilterToggleChanged",field:"weightingEnabled",raw:weighting.input.checked});act(workspace.commitField(weightingTarget));});
      const categoryGrid=node("div");categoryGrid.className="badge-rule-grid";categories.append(categoryGrid);
      const categoryFields:Array<{spec:typeof badgeEditorFields[number];input:HTMLInputElement;error:HTMLElement;count:HTMLElement;target:FieldTarget}>=[];
      for(const spec of badgeEditorFields){
        const row=node("div");row.className="badge-rule-row";row.dataset.badgeCategory=spec.category;categoryGrid.append(row);
        if(spec.category==="anonymous")row.append(checks[0]!.input.parentElement!);
        else {
          const value=field(row,badgeCategoryLabels[spec.category]+" 제외","checkbox","create-category-"+spec.category+"-excluded");const target:FieldTarget={_tag:"filterToggle",field:spec.excluded};checks.push({input:value.input,error:value.error,target});
          yield* dom.listen(value.input,"change",()=>{workspace.input({_tag:"FilterToggleChanged",field:spec.excluded,raw:value.input.checked});act(workspace.commitField(target));});
        }
        const value=field(row,badgeCategoryLabels[spec.category]+" 당첨 비율","text","create-category-"+spec.category+"-weight");value.input.inputMode="numeric";value.input.maxLength=3;value.input.setAttribute("aria-description","0부터 100까지의 상대 비율. 분류 안에서는 개인별 균등 추첨");
        const target:FieldTarget={_tag:"filterText",field:spec.weight};yield* bind(value.input,target,(raw)=>({_tag:"FilterTextChanged",field:spec.weight,raw}));const count=node("small");count.hidden=true;row.append(count);categoryFields.push({spec,input:value.input,error:value.error,count,target});
      }
      categories.append(node("p","비율을 끄면 모든 포함 참가자의 기회가 같습니다. 켜면 후보가 남은 분류끼리 상대 비율을 나누고, 분류 안에서는 균등하게 뽑습니다. 비율이 0인 분류는 당첨 후보에서 빠지며, 후보가 소진된 분류의 몫은 다시 나눕니다."));
      const authorHint=node("p");filters.append(authorHint);
      const time=node("div");time.className="create-inline filter-time-fields"; filters.append(time);
      const timeFields:Array<{input:HTMLInputElement;binding:CreateInputBinding;name:"days"|"hour"|"minute"}>=[];
      for(const [name,label,max] of [["days","며칠 전",365],["hour","시 (KST)",23],["minute","분",59]] as const) {
        const value=field(time,label);value.input.inputMode="numeric";value.input.setAttribute("aria-description","0부터 "+max+"까지");
        const target:FieldTarget={_tag:"filterText",field:"timeCut"};
        const binding=yield* bindCreateTextInput(value.input,{readRaw:()=>workspace.read().timeRaw[name],writeRaw:(raw)=>workspace.timeInput(name,raw),candidate:()=>{const editor=workspace.read().editor;return editor===null||workspace.read().writesBlocked?null:{identity:editor.identity,editorEpoch:editor.editorEpoch,inputVersion:editor.filters.timeCut.inputVersion};},onDebounced:(fence)=>write(workspace.commitField(target,fence)),onSubmit:write(workspace.commitField(target))},workspace.dispatch,workspace.report);timeFields.push({input:value.input,binding,name});
      }
      const timeHint=node("p");filters.append(timeHint);
      filters.append(node("p","지정한 분까지 작성된 댓글을 포함합니다. 댓글 시간이 확인되지 않으면 시간컷을 적용할 수 없습니다."));
      const keywordGroups:Array<{kind:"include"|"exclude";input:HTMLInputElement;binding:CreateInputBinding;tags:AwaitedKeywordRows}>=[];
      type AwaitedKeywordRows={patch:(models:ReadonlyArray<string>)=>Effect.Effect<void,unknown,DomPlatform|Scope.Scope>};
      const keywordGrid=node("div");keywordGrid.className="keyword-grid";filters.append(keywordGrid);
      for(const [kind,label] of [["include","포함 단어"],["exclude","제외 단어"]] as const) {
        const box=node("section");keywordGrid.append(box);const entry=node("div");entry.className="keyword-entry";box.append(entry);const value=field(entry,label);value.input.maxLength=100;
        const binding=yield* bindCreateTextInput(value.input,{readRaw:()=>workspace.read().keywordRaw[kind],writeRaw:(raw)=>workspace.setKeywordRaw(kind,raw),candidate:()=>null,onDebounced:()=>Effect.void,onSubmit:write(workspace.addKeyword(kind))},workspace.dispatch,workspace.report);
        const addKeyword=yield* button(entry,"단어 추가",()=>act(workspace.addKeyword(kind)),true);addKeyword.className="ui-button-small";
        const list=node("div");box.append(list);
        const tags=yield* makeKeyedRows(list,100,(word:string)=>word,(word)=>Effect.gen(function*(){const row=node("span");row.className="create-tag";const text=node("span",word);row.append(text);const removeKeyword=yield* button(row,word+" 삭제",()=>act(workspace.removeKeyword(kind,word)),true);removeKeyword.className="ui-button-ghost ui-button-small";return {element:row,value:{patch:()=>{}}};}));
        keywordGroups.push({kind,input:value.input,binding,tags});
      }
      const filterActions=node("div");filterActions.className="ui-actions";filters.append(filterActions);
      const resetFilters=yield* button(filterActions,"필터 초기화",()=>act(workspace.resetFilters),true);resetFilters.className="ui-button-ghost";
      const filterFailure=node("p");filterFailure.id="create-filter-error";filterFailure.className="create-filter-error";filterFailure.setAttribute("role","alert");filterFailure.dataset.filterFailure="";filterFailure.hidden=true;root.append(filterFailure);
      // A retry of an admitted version observes its existing receipt. The
      // Coordinator still rejects every new mutation while writes are blocked.
      const retryActions=node("div");retryActions.className="ui-actions";filters.append(retryActions);
      const retryFilters=yield* button(retryActions,"필터 다시 적용",()=>act(workspace.commitField({_tag:"filterToggle",field:"excludeAnonymous"})));retryFilters.className="ui-button-ghost";retryFilters.hidden=true;

      const layout=node("div");layout.className="create-workspace";root.append(layout);const selection=node("section");selection.className="create-selection app-card";selection.append(node("h2","참가자"));layout.append(selection);
      const counts=node("p");selection.append(counts);
      const controls=node("div");controls.className="create-inline participant-toolbar";selection.append(controls);
      const groupField=node("div");groupField.className="ui-field";controls.append(groupField);
      const groupLabel=node("label","분류"); const group=node("select");group.id="create-group";groupLabel.htmlFor=group.id;
      for(const [value,label] of [["unclassified","미분류"],["included","자동 포함"],["excluded","자동 제외"]] as const){const option=node("option",label);option.value=value;group.append(option);}groupField.append(groupLabel,group);
      const search=field(controls,"참가자 검색","search"); search.input.maxLength=100;
      const previewLabel=node("label","참가자 보기 방식");const previewMode=node("select");previewMode.id="create-preview-mode";previewLabel.htmlFor=previewMode.id;
      const previewField=node("div");previewField.className="ui-field";controls.append(previewField);
      for(const [value,label]of [["comments","댓글도 보기"],["nicknames","닉네임만 보기"]]as const){const option=node("option",label);option.value=value;previewMode.append(option);}previewField.append(previewLabel,previewMode);let showComments=true;
      yield* dom.listen(previewMode,"change",()=>{showComments=previewMode.value==="comments";act(frame.offer(undefined));});
      const participantTools=node("div");participantTools.className="ui-actions participant-tools";selection.append(participantTools);
      const allInclude=yield* button(participantTools,"미분류 모두 포함",()=>act(workspace.setUnclassified(true)),true);
      const allExclude=yield* button(participantTools,"미분류 모두 제외",()=>act(workspace.setUnclassified(false)),true);
      const resetManual=yield* button(participantTools,"수동 분류 초기화",()=>act(workspace.resetManual),true);resetManual.className="ui-button-ghost";
      const queryStatus=node("p");queryStatus.setAttribute("role","status");selection.append(queryStatus);
      const rowsHost=node("ul");rowsHost.className="create-participants";selection.append(rowsHost);
      const pages=node("div");pages.className="create-inline ui-actions participant-page-actions";selection.append(pages);
      let querySearch:ParticipantSearch={group:"unclassified",search:"",offset:0}; let searchRaw="";
      let screen:CreateScreenOwner<never>;
      const fullPages=new Map<string,typeof ParticipantsPage.Type>();
      const queries={query:(request:ParticipantRequest)=>commands.queryParticipants({...request,query:request.search}).pipe(Effect.flatMap((reply)=>Effect.gen(function*(){
        const current=yield* screen.snapshot;
        if(screenAcceptsRequest(current,request)){fullPages.set(pageKey(request),reply.data);while(fullPages.size>3){const oldest=fullPages.keys().next().value;if(oldest!==undefined)fullPages.delete(oldest);}}
        return {context:reply.data.context,group:request.group,search:request.search,offset:reply.data.offset,rows:reply.data.rows.map((row)=>({participantId:row.id,displayName:row.nickname,group:request.group})),groupCount:reply.data.total,matchCount:reply.data.matched};
      }))),messageKey:(cause:Cause.Cause<unknown>)=>"TransportError" as const};
      screen=yield* mountCreateScreenModel({route:{_tag:"create"},generation:0,isCurrent:()=>owner.state._tag!=="Closed"},context(workspace.read().draft),queries);
      const query=(changes:Partial<ParticipantSearch>={})=>{querySearch={...querySearch,...changes};act(screen.query(querySearch));};
      yield* dom.listen(group,"change",()=>query({group:group.value as ParticipantGroup,offset:0}));
      yield* bindCreateTextInput(search.input,{readRaw:()=>searchRaw,writeRaw:(raw)=>{searchRaw=raw;},candidate:()=>({identity:{backendSessionId:"search",draftId:"search"},editorEpoch:0,inputVersion:searchRaw.length}),onDebounced:()=>Effect.sync(()=>query({search:searchRaw,offset:0})),onSubmit:Effect.sync(()=>query({search:searchRaw,offset:0}))},workspace.dispatch,workspace.report);
      const previous=yield* button(pages,"이전 100명",()=>query({offset:Math.max(0,querySearch.offset-100)}));previous.className="ui-button-ghost ui-button-small";
      const pageLabel=node("span");pages.append(pageLabel);
      const next=yield* button(pages,"다음 100명",()=>query({offset:querySearch.offset+100}));next.className="ui-button-ghost ui-button-small";
      let detailsOpen=false;
      const details=(participant:Participant)=>Effect.suspend(()=>{
        if(detailsOpen)return Effect.void;
        detailsOpen=true;
        return Effect.gen(function*(){
        const draft=workspace.read().draft;if(draft===null)return;
        const expected=context(draft);if(expected===null)return;
        const scope=yield* Scope.fork(owner,"sequential");let session:DialogSession;
        let pageOffset=0;let generation=0;
        session=yield* mountDialog(root,Effect.gen(function*(){
          const dialog=node("dialog");const title=node("h2",participant.nickname+" 댓글");title.id="participant-detail-title";dialog.append(title);const content=node("div");dialog.append(content);
          const contentRows=yield* makeKeyedRows(content,50,(comment:typeof CommentsPage.Type.rows[number])=>comment.id,(comment)=>makeCommentRow(comment,root.ownerDocument,value=>(value.parentId===null?"":"↳ 답글")+(value.postedAt===null?"":" · "+value.postedAt)));
          const info=node("p");dialog.append(info);
          const footer=node("div");footer.className="ui-actions";dialog.append(footer);
          const fetch=()=>Effect.gen(function*(){const token=++generation;const requested=pageOffset;const reply=yield* commands.queryParticipantComments({...expected,participantId:participant.id,offset:requested,limit:50});const current=context(workspace.read().draft);if(scope.state._tag==="Closed"||token!==generation||JSON.stringify(current)!==JSON.stringify(expected))return;yield* contentRows.patch(reply.data.rows);patchText(info,reply.data.total===0?"댓글 없음":(requested+1)+"–"+(requested+reply.data.rows.length)+" / "+reply.data.total);patchDisabled(back,requested===0);patchDisabled(forward,requested+50>=reply.data.total);});
          const back=yield* button(footer,"이전 댓글",()=>{pageOffset=Math.max(0,pageOffset-50);act(fetch().pipe(Scope.provide(scope)));});back.className="ui-button-ghost ui-button-small";
          const forward=yield* button(footer,"다음 댓글",()=>{pageOffset+=50;act(fetch().pipe(Scope.provide(scope)));});forward.className="ui-button-ghost ui-button-small";
          const close=yield* button(footer,"닫기",()=>act(session.close));close.className="ui-button-ghost";
          yield* fetch();return {element:dialog,value:{title,initialFocus:close,sensitiveInputs:[]}};
        })).pipe(Scope.provide(scope),Effect.onExit((exit)=>Exit.isFailure(exit)?Scope.close(scope,exit):Effect.void));
        yield* session.closed;yield* Scope.close(scope,Exit.void);
        }).pipe(Effect.ensuring(Effect.sync(()=>{detailsOpen=false;})));
      });
      const participantRows=yield* makeKeyedRows(rowsHost,100,(row:Participant)=>row.id,(initial)=>Effect.gen(function*(){
        let current=initial;const row=node("li");const toggle=field(row,"포함","checkbox");const badge=node("span");badge.className="participant-badge";const description=node("span");row.append(badge,description);
        yield* dom.listen(toggle.input,"change",()=>{if(!workspace.read().writesBlocked)act(workspace.toggleParticipant(current.id));});
        const comments=yield* button(row,"댓글 보기",()=>act(details(current)));comments.className="ui-button-ghost ui-button-small";
        const patch=(value:Participant)=>{current=value;const category=participantCategory(value);badge.dataset.category=category;patchText(badge,badgeCategoryLabels[category]);patchText(description,value.nickname+" ("+value.publicIdentifier+") · "+value.commentCount+"개 · "+reasons[value.reason]+(showComments?" "+value.previews.join(" · "):""));toggle.input.checked=value.included;toggle.input.setAttribute("aria-label",value.nickname+" 참가자 포함");patchDisabled(toggle.input,workspace.read().writesBlocked||workspace.read().editor?.lock!==null);};patch(initial);return {element:row,value:{patch}};
      }));

      const prizes=node("fieldset");prizes.className="create-prizes app-card";prizes.append(node("legend","상품과 추첨 방식"));layout.append(prizes);
      const prizeSettings=node("div");prizeSettings.className="prize-settings";prizes.append(prizeSettings);
      const select=(title:string,options:ReadonlyArray<readonly[string,string]>)=>{const box=node("div");box.className="ui-field";const label=node("label",title);const element=node("select");element.id="create-field-"+(++serial);label.htmlFor=element.id;for(const [value,text]of options){const option=node("option",text);option.value=value;element.append(option);}box.append(label,element);prizeSettings.append(box);return element;};
      const drawMode=select("추첨 방식",[["immediate","즉시 추첨"],["reservation","예약 추첨"]]);const prizeMode=select("상품 구성",[["single","단일 상품"],["multiple","여러 상품"]]);
      yield* dom.listen(drawMode,"change",()=>{workspace.input({_tag:"DrawModeChanged",raw:drawMode.value==="reservation"?"reservation":"immediate"});act(workspace.commitField({_tag:"drawMode"}));});
      yield* dom.listen(prizeMode,"change",()=>{workspace.input({_tag:"PrizeModeChanged",raw:prizeMode.value==="multiple"?"multiple":"single"});act(workspace.commitField({_tag:"prizeMode"}));});
      const single=node("div");single.className="prize-row";prizes.append(single);const singleName=field(single,"상품명 (선택)");singleName.input.maxLength=20;const singleCountGroup=node("div");singleCountGroup.className="prize-count-group";single.append(singleCountGroup);const singleCount=field(singleCountGroup,"당첨 인원");singleCount.input.inputMode="numeric";
      yield* bind(singleName.input,{_tag:"singleName"},(raw)=>({_tag:"SingleNameChanged",raw}));yield* bind(singleCount.input,{_tag:"singleCount"},(raw)=>({_tag:"SingleCountChanged",raw}));
      const adjustCount=(delta:number)=>{const state=workspace.read();const editor=state.editor;if(editor===null||editor.lock!==null)return;const raw=editor.single.count.raw;const value=Number(raw);const next=value+delta;if(!/^\d+$/.test(raw)||!Number.isSafeInteger(value)||next<1||next>10||delta>0&&next>Math.min(10,state.draft?.included??0))return;workspace.input({_tag:"SingleCountChanged",raw:String(next)});act(write(workspace.commitField({_tag:"singleCount"})));};
      const countActions=node("div");countActions.className="ui-actions prize-count-actions";singleCountGroup.append(countActions);
      const decrease=yield* button(countActions,"당첨 인원 줄이기",()=>adjustCount(-1),true);decrease.className="ui-button-small";decrease.textContent="−";decrease.setAttribute("aria-label","당첨 인원 줄이기");decrease.title="당첨 인원 줄이기";const increase=yield* button(countActions,"당첨 인원 늘리기",()=>adjustCount(1),true);increase.className="ui-button-small";increase.textContent="+";increase.setAttribute("aria-label","당첨 인원 늘리기");increase.title="당첨 인원 늘리기";
      const multiple=node("div");prizes.append(multiple);
      const prizeRows=yield* makeKeyedRows(multiple,10,(prize:PrizeInput)=>prize.id,(prize)=>Effect.gen(function*(){
        const row=node("div");row.className="create-inline prize-row";const name=field(row,"상품명 (선택)");name.input.maxLength=20;const count=field(row,"당첨 인원");count.input.inputMode="numeric";
        const nameTarget:FieldTarget={_tag:"multipleName",prizeId:prize.id};const countTarget:FieldTarget={_tag:"multipleCount",prizeId:prize.id};
        const nameBinding=yield* bind(name.input,nameTarget,(raw)=>({_tag:"MultipleNameChanged",prizeId:prize.id,raw}));const countBinding=yield* bind(count.input,countTarget,(raw)=>({_tag:"MultipleCountChanged",prizeId:prize.id,raw}));
        const remove=yield* button(row,"상품 삭제",()=>{if(!workspace.read().structuralPending)act(workspace.removePrize(prize.id));},true);remove.className="ui-button-danger ui-button-small";
        yield* Effect.addFinalizer(()=>Effect.sync(()=>{for(let i=bindings.length-1;i>=0;i--)if(bindings[i]?.input===name.input||bindings[i]?.input===count.input)bindings.splice(i,1);}));
        const patch=(value:PrizeInput)=>{const nameState=nameBinding.snapshot();const countState=countBinding.snapshot();patchInputValue(name.input,value.name.raw,nameState._tag==="open"&&nameState.composition==="composing");patchInputValue(count.input,value.count.raw,countState._tag==="open"&&countState.composition==="composing");patchDisabled(remove,workspace.read().writesBlocked||workspace.read().structuralPending||workspace.read().editor?.lock!==null||workspace.read().editor?.multiple.length===1);};patch(prize);return {element:row,value:{patch}};
      }));
      const prizeActions=node("div");prizeActions.className="ui-actions";prizes.append(prizeActions);
      const addPrize=yield* button(prizeActions,"상품 추가",()=>{if(!workspace.read().structuralPending)act(workspace.addPrize);},true);addPrize.className="ui-button-ghost";
      const prizeHint=node("p","상품은 최대 10개, 전체 당첨 인원은 포함된 참가자 수와 10명 이하입니다. 다른 구성으로 바꿔도 입력값은 유지됩니다.");prizes.append(prizeHint);

      let creating=false;
      const creationBlocked=()=>{const value=workspace.read();return value.writesBlocked||creating||value.editor===null||value.editor.lock!==null||value.draft?.article===null||(value.draft?.included??0)<2||value.draft?.load?.state==="loading"||value.draft?.load?.state==="cancelling";};
      const createFooter=node("div");createFooter.className="ui-actions create-footer";prizes.insertBefore(createFooter,prizeHint);
      const create=yield* button(createFooter,"추첨 생성",()=>{if(creating||workspace.read().writesBlocked)return;creating=true;act(Effect.gen(function*(){
        const prepared=yield* workspace.prepareCreation;
        const result=yield* workspace.finalize(prepared);
        if(owner.state._tag!=="Closed")goResult(result.collectionId);
      }).pipe(Effect.ensuring(Effect.sync(()=>{creating=false;if(owner.state._tag!=="Closed"&&create.isConnected)patchDisabled(create,creationBlocked());}))));});create.className="ui-button-primary";
      const patch=Effect.gen(function*(){
        const state=workspace.read();const editor=state.editor;const draft=state.draft;const blocked=state.writesBlocked;
        const dirtyFilters=editor===null?[]:Object.values(editor.filters).filter(field=>field.dirty);
        const filterFailed=dirtyFilters.some(field=>field.validation._tag==="invalid"&&(field.validation.messageKey==="SubmissionRejected"||field.validation.messageKey==="ReceiptExpired"));
        const localFilterInvalid=dirtyFilters.some(field=>field.validation._tag==="invalid"&&(field.validation.messageKey==="InvalidInput"||field.validation.messageKey==="TooManyWinners"));
        for(const element of writingButtons)patchDisabled(element,blocked||editor===null||editor.lock!==null);
        patchText(alert,state.error??"");alert.hidden=state.error===null;
        const loading=draft?.load?.state==="loading"||draft?.load?.state==="cancelling";
        patchText(status,loading?"수집 중: "+draft?.load?.pages+"페이지 · "+draft?.load?.comments+"개 댓글":draft?.load?.state==="failed"?"게시글 수집에 실패했습니다. 주소와 연결 상태를 확인해 주세요.":draft?.load?.state==="cancelled"?"수집을 취소했습니다.":draft?.load?.state==="completed"?"게시글 수집 완료":"게시글 주소를 입력해 주세요.");
        patchText(articleTitle,draft?.article===null||draft?.article===undefined?"":draft.article.title+" · "+draft.article.galleryName);articleTitle.hidden=draft?.article===null||draft?.article===undefined;
        patchAria(root,"aria-busy",loading||blocked?"true":null);patchDisabled(url.input,editor===null||(editor!==null&&isEditorFieldLocked(editor,{_tag:"url"})));patchDisabled(load,blocked||editor===null||editor.lock!==null);patchDisabled(cancel,!loading);cancel.hidden=!loading;patchDisabled(resetArticle,blocked||editor===null||editor.lock!==null);
        patchText(authorHint,draft?.authorIdentifiable?"":"작성자를 식별할 수 없어 작성자 제외가 적용되지 않습니다.");
        const cutoff=draft?.filters.timeCut;patchText(timeHint,cutoff===undefined||cutoff===null?"":"확정 시간컷: "+new Intl.DateTimeFormat("ko-KR",{timeZone:"Asia/Seoul",year:"numeric",month:"2-digit",day:"2-digit",hour:"2-digit",minute:"2-digit",hourCycle:"h23"}).format(new Date(Date.parse(cutoff)-60000))+"까지 (한국 시간)");
        if(editor!==null){
          const countValue=Number(editor.single.count.raw);const validCount=/^\d+$/.test(editor.single.count.raw)&&Number.isSafeInteger(countValue)&&countValue>=1&&countValue<=10;patchDisabled(decrease,blocked||editor.lock!==null||!validCount||countValue<=1);patchDisabled(increase,blocked||editor.lock!==null||!validCount||countValue>=Math.min(10,draft?.included??0));
          const active=editor.prizeMode.raw==="single"?[editor.single]:editor.multiple;const total=active.reduce((sum,item)=>sum+(Number(item.count.raw)||0),0);patchText(prizeHint,total>(draft?.included??0)?"당첨 인원이 포함된 참가자 수를 넘습니다. 인원을 줄여 주세요.":"상품은 최대 10개, 전체 당첨 인원은 포함된 참가자 수와 10명 이하입니다. 다른 구성으로 바꿔도 입력값은 유지됩니다.");
          for(const value of bindings){const field=getEditorField(editor,value.target);const snapshot=value.binding.snapshot();patchInputValue(value.input,String(field.raw),snapshot._tag==="open"&&snapshot.composition==="composing");patchDisabled(value.input,isEditorFieldLocked(editor,value.target)||(value.target._tag==="filterToggle"&&value.target.field==="excludeAuthor"&&!draft?.authorIdentifiable));}
          for(const value of checks){const field=getEditorField(editor,value.target);value.input.checked=Boolean(field.raw);patchDisabled(value.input,blocked||isEditorFieldLocked(editor,value.target)||(value.target._tag==="filterToggle"&&value.target.field==="excludeAuthor"&&!draft?.authorIdentifiable));patchFieldError(value.input,value.error,field.dirty&&field.validation._tag==="invalid"?(localFilterInvalid?"필터 입력값을 수정해 주세요.":"이 필터 변경을 아직 확인하지 못했습니다. 필터 다시 적용을 선택해 주세요."):null);}
          for(const value of categoryFields){const field=editor.filters[value.spec.weight];patchDisabled(value.input,!editor.filters.weightingEnabled.raw||editor.filters[value.spec.excluded].raw||isEditorFieldLocked(editor,value.target));patchFieldError(value.input,value.error,field.dirty&&field.validation._tag==="invalid"?(localFilterInvalid?"비율은 0부터 100까지의 정수로 입력해 주세요.":"이 필터 변경을 아직 확인하지 못했습니다."):null);}
          for(const value of timeFields){const snapshot=value.binding.snapshot();patchInputValue(value.input,state.timeRaw[value.name],snapshot._tag==="open"&&snapshot.composition==="composing");patchDisabled(value.input,!editor.filters.timeCutEnabled.raw||isEditorFieldLocked(editor,{_tag:"filterText",field:"timeCut"}));}
          for(const value of keywordGroups){const snapshot=value.binding.snapshot();patchInputValue(value.input,state.keywordRaw[value.kind],snapshot._tag==="open"&&snapshot.composition==="composing");patchDisabled(value.input,isEditorFieldLocked(editor,{_tag:"filterText",field:value.kind==="include"?"includeKeywords":"excludeKeywords"}));yield* value.tags.patch(editor.filters[value.kind==="include"?"includeKeywords":"excludeKeywords"].raw.split("\n").filter((word)=>word!==""));}
          if(drawMode.value!==editor.drawMode.raw)drawMode.value=editor.drawMode.raw;if(prizeMode.value!==editor.prizeMode.raw)prizeMode.value=editor.prizeMode.raw;drawMode.disabled=blocked||isEditorFieldLocked(editor,{_tag:"drawMode"});prizeMode.disabled=blocked||state.structuralPending||isEditorFieldLocked(editor,{_tag:"prizeMode"});
          single.hidden=editor.prizeMode.raw!=="single";multiple.hidden=editor.prizeMode.raw!=="multiple";addPrize.hidden=multiple.hidden;patchDisabled(addPrize,blocked||state.structuralPending||editor.lock!==null||editor.multiple.length>=10);yield* prizeRows.patch(editor.multiple);
        }
        const filterProblem=filterFailed||localFilterInvalid;
        if(filterProblem&&filterFailure.hidden)filterAccordion.open=true;
        filterFailure.hidden=!filterProblem;patchText(filterHint,filterProblem?"확인 필요":"선택 설정");patchAria(filterSummary,"aria-describedby",filterProblem?filterFailure.id:null);retryFilters.hidden=!filterFailed||localFilterInvalid;
        patchText(filterFailure,localFilterInvalid?"필터 입력값을 수정해 주세요. 참가자 분류는 마지막 확인 값을 표시합니다.":filterFailed?"필터 변경을 아직 확인하지 못했습니다. 참가자 분류는 마지막 확인 값을 표시합니다.":"");
        const observesOnly=dirtyFilters.length>0&&dirtyFilters.every(field=>field.submitted?.inputVersion===field.inputVersion);
        patchDisabled(retryFilters,!filterFailed||localFilterInvalid||editor===null||editor.lock!==null||(blocked&&!observesOnly));
        patchDisabled(resetFilters,blocked||editor===null||editor.lock!==null);for(const element of [allInclude,allExclude,resetManual])patchDisabled(element,blocked||editor===null||editor.lock!==null||draft?.article===null);
        patchDisabled(create,creationBlocked());
        patchText(counts,"전체 "+(draft?.participants??0)+"명 · 포함 "+(draft?.included??0)+"명 · 제외 "+(draft?.excluded??0)+"명");
        const model=yield* screen.snapshot;
        if(model._tag==="open"){
          const page=model.visiblePage;const payload=page===null?undefined:fullPages.get(pageKey({...page.context,...page}));
          yield* participantRows.patch(payload?.rows??[]);
          const complete=payload!==undefined&&draft!==null&&payload.offset===0&&payload.total===draft.participants&&payload.matched===draft.participants&&payload.rows.length===draft.participants;
          for(const value of categoryFields){value.count.hidden=!complete;if(complete){const rows=payload.rows.filter(row=>participantCategory(row)===value.spec.category);patchText(value.count,"전체 "+rows.length+"명 · 포함 "+rows.filter(row=>row.included).length+"명");}}
          patchText(queryStatus,model.query._tag==="loading"?"참가자 조회 중…":model.query._tag==="failed"?"참가자를 불러오지 못했습니다. 검색을 다시 실행해 주세요.":page?.matchCount===0?"일치하는 참가자가 없습니다.":"");
          patchText(pageLabel,page===null?"":"그룹 전체 "+page.groupCount+"명 · 검색 결과 "+page.matchCount+"명 · "+(page.matchCount===0?"0명":(page.offset+1)+"–"+(page.offset+page.rows.length)+" / "+page.matchCount+"명"));patchDisabled(previous,page===null||page.offset===0);patchDisabled(next,page===null||page.offset+100>=page.matchCount);
        }
      });
      const frame=yield* makeFramePatcher<void,unknown,DomPlatform|Scope.Scope>(()=>patch,workspace.report);
      yield* workspace.changes.pipe(Stream.runForEach((state)=>Effect.gen(function*(){
        const before=yield* screen.snapshot;yield* screen.updateContext(context(state.draft));const after=yield* screen.snapshot;
        if(after._tag==="open"&&after.context!==null&&(before._tag==="closed"||before.context?.revision!==after.context.revision||before.context?.draftId!==after.context.draftId||before.context?.backendSessionId!==after.context.backendSessionId)){fullPages.clear();yield* screen.query({...querySearch,offset:0});}
        yield* frame.offer(undefined);
      })),Effect.catchCause(workspace.report),Effect.forkScoped);
      yield* screen.changes.pipe(Stream.runForEach(()=>frame.offer(undefined)),Effect.catchCause(workspace.report),Effect.forkScoped);
      yield* Effect.addFinalizer(()=>Effect.sync(()=>{fullPages.clear();bindings.length=0;searchRaw="";}));
      if(workspace.read().draft!==null)yield* screen.query(querySearch);yield* patch;act(workspace.ensure);
      return {element:root,value:{}};
    })).pipe(Scope.provide(owner),Effect.onExit((exit)=>Exit.isFailure(exit)?Scope.close(owner,exit):Effect.void));
  });
}
