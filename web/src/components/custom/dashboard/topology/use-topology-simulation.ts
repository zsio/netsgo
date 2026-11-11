import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from 'd3-force';
import { select } from 'd3-selection';
import { zoom, zoomIdentity, type ZoomBehavior } from 'd3-zoom';
import { drag } from 'd3-drag';
import 'd3-transition';

import {
  SERVER_NODE_ID,
  computeQuadraticEdge,
  type TopologyEdge,
  type TopologyNode,
} from './topology-model';
import { initialNodePosition } from './topology-layout';

interface SimNode extends SimulationNodeDatum {
  id: string;
}

interface SimLink extends SimulationLinkDatum<SimNode> {
  kind: 'control' | 'c2c';
}

export function useTopologySimulation({
  effectiveFocusId,
  visibleNodes,
  visibleEdges,
  edgeOffsets,
}: {
  effectiveFocusId: string | null;
  visibleNodes: TopologyNode[];
  visibleEdges: TopologyEdge[];
  edgeOffsets: Map<string, number>;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const sceneRef = useRef<SVGGElement>(null);
  const simRef = useRef<Simulation<SimNode, undefined> | null>(null);
  const nodePosRef = useRef(new Map<string, SimNode>());
  const zoomRef = useRef<ZoomBehavior<SVGSVGElement, unknown> | null>(null);
  const pinnedIdRef = useRef<string>(SERVER_NODE_ID);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [layoutReady, setLayoutReady] = useState(false);

  useLayoutEffect(() => {
    const element = containerRef.current;
    if (!element) return;
    const observer = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      if (rect && rect.width > 0 && rect.height > 0) {
        setSize((previous) => {
          if (previous.width === rect.width && previous.height === rect.height) {
            return previous;
          }
          return { width: rect.width, height: rect.height };
        });
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // 结构签名：只有拓扑结构真正变化时才重启力导向模拟，
  // 避免周期性的 stats 刷新导致画面持续抖动。
  const structureSignature = useMemo(() => {
    const nodesPart = visibleNodes.map((node) => `${node.id}:${node.online ? 1 : 0}`).join(',');
    const edgesPart = visibleEdges.map((edge) => `${edge.id}:${edge.sourceId}>${edge.targetId}`).join(',');
    return `${nodesPart}|${edgesPart}`;
  }, [visibleNodes, visibleEdges]);

  const visibleNodesRef = useRef(visibleNodes);
  visibleNodesRef.current = visibleNodes;
  const visibleEdgesRef = useRef(visibleEdges);
  visibleEdgesRef.current = visibleEdges;
  const edgeOffsetsRef = useRef(edgeOffsets);
  edgeOffsetsRef.current = edgeOffsets;

  const { width, height } = size;

  // 缩放 / 平移
  useEffect(() => {
    const svgElement = svgRef.current;
    if (!svgElement) return;
    const svg = select(svgElement);
    const behavior = zoom<SVGSVGElement, unknown>()
      .scaleExtent([0.5, 2.5])
      .filter((event: MouseEvent | WheelEvent | TouchEvent) => {
        if (event.type === 'dblclick') return false;
        return !(event as MouseEvent).button;
      })
      .on('zoom', (event) => {
        select(sceneRef.current).attr('transform', event.transform.toString());
      });
    svg.call(behavior);
    zoomRef.current = behavior;
    return () => {
      svg.on('.zoom', null);
      zoomRef.current = null;
    };
  }, []);

  // 力导向模拟
  useLayoutEffect(() => {
    if (!width || !height) return;
    setLayoutReady(false);

    const cx = width / 2;
    const cy = height / 2;
    const ringRadius = Math.max(Math.min(width, height) / 2 - 86, 76);
    const nodePos = nodePosRef.current;

    const nodes = visibleNodesRef.current;
    const edges = visibleEdgesRef.current;
    const pinnedId = effectiveFocusId ?? SERVER_NODE_ID;
    const isOverviewLayout = pinnedId === SERVER_NODE_ID;
    const targetPositions = new Map<string, { x: number; y: number }>();
    for (const node of nodes) {
      targetPositions.set(
        node.id,
        initialNodePosition(node, nodes, pinnedId, width, height),
      );
    }
    const linkEndpointId = (endpoint: SimLink['source']) => {
      if (typeof endpoint === 'string') return endpoint;
      if (typeof endpoint === 'number') return String(endpoint);
      return endpoint?.id ?? '';
    };
    const targetDistance = (sourceId: string, targetId: string, fallback: number) => {
      const source = targetPositions.get(sourceId);
      const target = targetPositions.get(targetId);
      if (!source || !target) {
        return fallback;
      }
      return Math.max(72, Math.hypot(target.x - source.x, target.y - source.y));
    };
    // 进入聚焦态时保留旧坐标，让节点平滑移动；
    // 全览模式每次重启模拟都按目标重播种，避免旧焦点坐标污染扇形布局。
    const layoutChanged = pinnedIdRef.current !== pinnedId;
    const reseedAll = isOverviewLayout;
    pinnedIdRef.current = pinnedId;
    let disposed = false;
    let sceneSettled = false;

    const simNodes = nodes.map((node) => {
      let sim = nodePos.get(node.id);
      if (!sim) {
        const position = targetPositions.get(node.id)
          ?? initialNodePosition(node, nodes, pinnedId, width, height);
        sim = {
          id: node.id,
          x: position.x,
          y: position.y,
        };
        nodePos.set(node.id, sim);
      } else if (layoutChanged || reseedAll) {
        if (reseedAll) {
          const position = targetPositions.get(node.id)
            ?? initialNodePosition(node, nodes, pinnedId, width, height);
          sim.x = position.x;
          sim.y = position.y;
        }
        sim.vx = 0;
        sim.vy = 0;
      }
      sim.fx = null;
      sim.fy = null;
      return sim;
    });
    const pinned = nodePos.get(pinnedId);
    const pinnedTarget = targetPositions.get(pinnedId) ?? { x: cx, y: cy };
    if (pinned) {
      pinned.fx = pinnedTarget.x;
      pinned.fy = pinnedTarget.y;
    }
    if (!isOverviewLayout) {
      const server = nodePos.get(SERVER_NODE_ID);
      const serverTarget = targetPositions.get(SERVER_NODE_ID);
      if (server && serverTarget) {
        server.fx = serverTarget.x;
        server.fy = serverTarget.y;
      }
    }

    const simLinks: SimLink[] = [];
    for (const node of nodes) {
      if (node.kind === 'client' && (isOverviewLayout || node.id === pinnedId)) {
        simLinks.push({ source: SERVER_NODE_ID, target: node.id, kind: 'control' });
      }
    }
    const seenPairs = new Set<string>();
    for (const edge of edges) {
      if (edge.sourceId === SERVER_NODE_ID || edge.targetId === SERVER_NODE_ID) continue;
      const key = edge.sourceId < edge.targetId
        ? `${edge.sourceId}|${edge.targetId}`
        : `${edge.targetId}|${edge.sourceId}`;
      if (seenPairs.has(key)) continue;
      seenPairs.add(key);
      simLinks.push({ source: edge.sourceId, target: edge.targetId, kind: 'c2c' });
    }

    const ticked = () => {
      const scene = sceneRef.current;
      if (!scene) return;
      for (const sim of simNodes) {
        sim.x = Math.max(34, Math.min(width - 34, sim.x ?? cx));
        sim.y = Math.max(38, Math.min(height - 46, sim.y ?? cy));
      }
      const sceneSel = select(scene);
      sceneSel.select('[data-layer="nodes"]')
        .selectAll<SVGGElement, unknown>('[data-node-id]')
        .attr('transform', function positionNode() {
          const sim = nodePos.get(this.dataset.nodeId ?? '');
          return sim ? `translate(${sim.x},${sim.y})` : null;
        });

      const server = nodePos.get(SERVER_NODE_ID);
      sceneSel.selectAll<SVGPathElement, unknown>('[data-control-id]')
        .attr('d', function controlPath() {
          const client = nodePos.get(this.dataset.controlId ?? '');
          if (!client || !server) return null;
          return `M ${server.x} ${server.y} L ${client.x} ${client.y}`;
        });
      sceneSel.selectAll<SVGGElement, unknown>('[data-control-label]')
        .attr('transform', function positionControlLabel() {
          const client = nodePos.get(this.dataset.controlLabel ?? '');
          if (!client || !server) return null;
          const midpointX = ((server.x ?? cx) + (client.x ?? cx)) / 2;
          const midpointY = ((server.y ?? cy) + (client.y ?? cy)) / 2;
          return `translate(${midpointX},${midpointY})`;
        });

      const offsets = edgeOffsetsRef.current;
      const edgeList = visibleEdgesRef.current;
      const geometryById = new Map<string, ReturnType<typeof computeQuadraticEdge>>();
      const flowGeometryById = new Map<string, ReturnType<typeof computeQuadraticEdge>>();
      const flowEndpointsById = new Map<string, { x1: number; y1: number; x2: number; y2: number }>();
      for (const edge of edgeList) {
        const source = nodePos.get(edge.sourceId);
        const target = nodePos.get(edge.targetId);
        const flowSource = nodePos.get(edge.flowSourceId);
        const flowTarget = nodePos.get(edge.flowTargetId);
        if (!source || !target) continue;
        geometryById.set(edge.id, computeQuadraticEdge(
          edge.sourceId,
          edge.targetId,
          { x: source.x ?? cx, y: source.y ?? cy },
          { x: target.x ?? cx, y: target.y ?? cy },
          offsets.get(edge.id) ?? 0,
        ));
        if (flowSource && flowTarget) {
          flowEndpointsById.set(edge.id, {
            x1: flowSource.x ?? cx,
            y1: flowSource.y ?? cy,
            x2: flowTarget.x ?? cx,
            y2: flowTarget.y ?? cy,
          });
          flowGeometryById.set(edge.id, computeQuadraticEdge(
            edge.flowSourceId,
            edge.flowTargetId,
            { x: flowSource.x ?? cx, y: flowSource.y ?? cy },
            { x: flowTarget.x ?? cx, y: flowTarget.y ?? cy },
            offsets.get(edge.id) ?? 0,
          ));
        }
      }
      select(svgRef.current)
        .selectAll<SVGLinearGradientElement, unknown>('[data-edge-gradient]')
        .each(function updateFlowGradient() {
          const endpoints = flowEndpointsById.get(this.dataset.edgeGradient ?? '');
          if (!endpoints) return;
          select(this)
            .attr('x1', endpoints.x1)
            .attr('y1', endpoints.y1)
            .attr('x2', endpoints.x2)
            .attr('y2', endpoints.y2);
        });
      sceneSel.selectAll<SVGGElement, unknown>('[data-edge-id]').each(function updateEdge() {
        const geometry = geometryById.get(this.dataset.edgeId ?? '');
        if (!geometry) return;
        const flowGeometry = flowGeometryById.get(this.dataset.edgeId ?? '') ?? geometry;
        const group = select(this);
        group.selectAll('[data-edge-path="base"], [data-edge-path="hit"]').attr('d', geometry.path);
        group.selectAll('[data-edge-path="flow"]').attr('d', flowGeometry.path);
      });
      sceneSel.selectAll<SVGGElement, unknown>('[data-edge-label]')
        .attr('transform', function positionLabel() {
          const geometry = geometryById.get(this.dataset.edgeLabel ?? '');
          return geometry ? `translate(${geometry.midpoint.x},${geometry.midpoint.y})` : null;
        });

      if (!sceneSettled) {
        sceneSettled = true;
        if (!disposed) {
          setLayoutReady(true);
        }
      }
    };

    const simulation = forceSimulation<SimNode>(simNodes)
      .force('charge', forceManyBody<SimNode>().strength(isOverviewLayout ? -90 : -120))
      .force('collide', forceCollide<SimNode>(46))
      .force('link', forceLink<SimNode, SimLink>(simLinks)
        .id((node) => node.id)
        .distance((link) => targetDistance(
          linkEndpointId(link.source),
          linkEndpointId(link.target),
          link.kind === 'c2c' ? Math.min(180, ringRadius * 1.15) : ringRadius,
        ))
        .strength((link) => (
          link.kind === 'c2c'
            ? (isOverviewLayout ? 0.035 : 0.025)
            : (isOverviewLayout ? 0.025 : 0.028)
        )))
      .force('x', forceX<SimNode>((node) => targetPositions.get(node.id)?.x ?? cx).strength(isOverviewLayout ? 0.12 : 0.32))
      .force('y', forceY<SimNode>((node) => targetPositions.get(node.id)?.y ?? cy).strength(isOverviewLayout ? 0.12 : 0.36))
      .alpha(isOverviewLayout ? 0.24 : 0.32)
      .alphaDecay(0.16)
      .velocityDecay(0.65)
      .on('tick', ticked);

    simRef.current = simulation;
    ticked();

    return () => {
      disposed = true;
      simulation.stop();
      simRef.current = null;
    };
  }, [structureSignature, effectiveFocusId, width, height]);

  // 节点拖拽
  useEffect(() => {
    const svgElement = svgRef.current;
    if (!svgElement) return;
    const nodePos = nodePosRef.current;
    const behavior = drag<SVGGElement, unknown>()
      .clickDistance(5)
      .on('start', function dragStart(event) {
        event.sourceEvent?.stopPropagation();
        const sim = nodePos.get(this.dataset.nodeId ?? '');
        if (!sim) return;
        simRef.current?.alphaTarget(0.25).restart();
        sim.fx = sim.x;
        sim.fy = sim.y;
      })
      .on('drag', function dragMove(event) {
        const sim = nodePos.get(this.dataset.nodeId ?? '');
        if (!sim) return;
        sim.fx = event.x;
        sim.fy = event.y;
      })
      .on('end', function dragEnd() {
        simRef.current?.alphaTarget(0);
        const id = this.dataset.nodeId ?? '';
        const sim = nodePos.get(id);
        if (!sim) return;
        if (id !== pinnedIdRef.current) {
          sim.fx = null;
          sim.fy = null;
        }
      });
    select(svgElement)
      .selectAll<SVGGElement, unknown>('[data-node-id]')
      .call(behavior);
  }, [structureSignature]);

  const zoomBy = useCallback((factor: number) => {
    const svgElement = svgRef.current;
    if (!svgElement || !zoomRef.current) return;
    zoomRef.current.scaleBy(select(svgElement).transition().duration(200), factor);
  }, []);

  const resetView = useCallback(() => {
    const svgElement = svgRef.current;
    if (!svgElement || !zoomRef.current) return;
    zoomRef.current.transform(select(svgElement).transition().duration(280), zoomIdentity);
  }, []);

  return {
    containerRef,
    svgRef,
    sceneRef,
    layoutReady,
    zoomBy,
    resetView,
  };
}
