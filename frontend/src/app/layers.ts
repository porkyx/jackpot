import { Layer } from "effect";
import { Backend } from "../contracts/backend";
import { OperationCoordinatorLive } from "../operations/coordinator";
import { ResultImage } from "../platform/canvas";
import { DomPlatform, DomPlatformLive } from "../platform/dom";
import { ClientIds, ClientIdsLive } from "../platform/ids";

export interface AppDependencies<BE, BR, DE, DR, IE, IR, RE, RR> {
  readonly backend: Layer.Layer<Backend, BE, BR>;
  readonly dom: Layer.Layer<DomPlatform, DE, DR>;
  readonly ids: Layer.Layer<ClientIds, IE, IR>;
  readonly resultImage: Layer.Layer<ResultImage, RE, RR>;
}

// Both production and controlled tests use this composition. It constructs one
// Coordinator, and does not create services for ShellState or pure models.
export function makeAppLayer<BE, BR, DE, DR, IE, IR, RE, RR>(dependencies: AppDependencies<BE, BR, DE, DR, IE, IR, RE, RR>) {
  const platform = Layer.mergeAll(dependencies.backend, dependencies.dom, dependencies.ids, dependencies.resultImage);
  return OperationCoordinatorLive.pipe(Layer.provideMerge(platform));
}

export function makeAppLive<BE, BR, RE, RR>(backend: Layer.Layer<Backend, BE, BR>, resultImage: Layer.Layer<ResultImage, RE, RR>) {
  return makeAppLayer({ backend, resultImage, dom: DomPlatformLive, ids: ClientIdsLive });
}

// The current bootstrap screen uses reads, DOM and IDs. T07 supplies the actual
// renderer to makeAppLive when export becomes a product feature.
export function makeBootstrapLayer<BE, BR>(backend: Layer.Layer<Backend, BE, BR>) {
  return OperationCoordinatorLive.pipe(Layer.provideMerge(Layer.mergeAll(backend, DomPlatformLive, ClientIdsLive)));
}
