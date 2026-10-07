import { Effect } from "effect";
import { TransportError, type BackendError } from "../contracts/backend";
import type { ProductError } from "./productCoordinator";
// Product reads share the Coordinator's 15s / 250ms / 1s bounded policy.
// Transport and timeout are retried, protocol/domain failures are never retried.
export function readProductQuery<A>(query:()=>Effect.Effect<A,BackendError>):Effect.Effect<A,ProductError>{
 return Effect.gen(function*(){
  for(let attempt=0;attempt<3;attempt++){
   const result=yield* Effect.result(Effect.suspend(query).pipe(Effect.timeoutOrElse({duration:15000,orElse:()=>Effect.fail(new TransportError())})));
   if(result._tag==="Success")return result.success;
   if(result.failure._tag!=="TransportError"||attempt===2)return yield* Effect.fail(result.failure);
   yield* Effect.sleep(attempt===0?250:1000);
  }
  return yield* Effect.die("bounded product read escaped its terminal branch");
 });
}