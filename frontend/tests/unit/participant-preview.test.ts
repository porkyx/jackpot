import {expect,it} from "@effect/vitest";
import {Schema} from "effect";
import {ParticipantData,WinnerData,ParticipantsPage,FrozenParticipantsPage} from "../../src/contracts/product";
const participant={id:"p",nickname:"이름",publicIdentifier:"user",kind:"fixed",classification:"included",reason:"",included:true,commentCount:4,previews:[]};
const decode=Schema.decodeUnknownSync(ParticipantData);
for(const [label,previews]of [["none",[]],["empty text",[""]],["one",["one"]],["three",["one","two","three"]],["256 UTF16",["가".repeat(256)]],["128 astral codepoints",["😀".repeat(128)]],["literal markup",["<script>literal</script>"]]]as const){
 it("participant previews accept "+label+" without inventing full comment data",()=>{expect(decode({...participant,previews}).previews).toEqual(previews);});
}
for(const [label,previews]of [["missing",undefined],["null",null],["string","one"],["four",["1","2","3","4"]],["257 UTF16",["가".repeat(257)]],["129 astral codepoints",["😀".repeat(129)]],["null entry",[null]],["numeric entry",[1]],["object entry",[{text:"one"}]] ]as const){
 it("participant previews reject "+label+" before crossing the typed boundary",()=>{expect(()=>decode({...participant,previews})).toThrow();});
}
it("legacy singular preview cannot silently satisfy the required previews contract",()=>{const {previews:_old,...legacy}=participant;expect(()=>decode({...legacy,preview:"one"})).toThrow();});
it("draft frozen and winner projections enforce the same bounded participant contract",()=>{const valid={...participant,previews:["first","second","third"]};expect(Schema.decodeUnknownSync(WinnerData)({participant:valid,prizeId:"prize",prizeName:"",slot:1}).participant.previews).toHaveLength(3);const bad={...valid,previews:["1","2","3","4"]};for(const schema of [ParticipantsPage,FrozenParticipantsPage])expect(()=>Schema.decodeUnknownSync(schema)({context:{backendSessionId:"s",draftId:"d",revision:1,articleGeneration:1},collectionId:"c",revision:1,total:1,matched:1,offset:0,rows:[bad]})).toThrow();});