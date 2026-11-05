// The canvas (ADR-0032): a blueprint's graph, for an item's topology, the
// composite designer and a request's run.
export { Canvas, NodeDetails, type CanvasProps, type CanvasView } from "./Canvas";
export { CanvasList } from "./CanvasList";
export { blueprintGraph, compositeGraph, vmGraph } from "./blueprint";
export {
  addProblems,
  describeNode,
  nodeForPointer,
  statusLabel,
  within,
  type CanvasDirection,
  type CanvasEdge,
  type CanvasEdgeKind,
  type CanvasGraph,
  type CanvasGroup,
  type CanvasLine,
  type CanvasNode,
  type CanvasNodeKind,
  type CanvasStatus,
} from "./graph";
export { layoutGraph, nearest, type CanvasLayout } from "./layout";
