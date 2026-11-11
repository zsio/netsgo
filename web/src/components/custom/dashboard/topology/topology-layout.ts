import { SERVER_NODE_ID, type TopologyNode } from './topology-model';

export const OVERVIEW_FAN_CLIENT_LIMIT = 9;
export const FOCUS_FAN_CLIENT_LIMIT = 8;

function clamp(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}

function lerp(start: number, end: number, t: number) {
  return start + (end - start) * clamp(t, 0, 1);
}

function radians(degrees: number) {
  return (degrees / 180) * Math.PI;
}

export function overviewServerPosition(width: number, height: number) {
  return {
    x: width / 2,
    y: clamp(height * 0.3, 88, Math.max(88, height * 0.36)),
  };
}

export function overviewFanPosition(
  node: TopologyNode,
  clients: TopologyNode[],
  width: number,
  height: number,
) {
  const server = overviewServerPosition(width, height);
  if (node.id === SERVER_NODE_ID) {
    return server;
  }

  const index = Math.max(0, clients.findIndex((candidate) => candidate.id === node.id));
  const count = Math.max(1, clients.length);
  const fanT = count <= 2 ? 0 : (count - 2) / (OVERVIEW_FAN_CLIENT_LIMIT - 2);
  const spread = radians(count === 1 ? 0 : lerp(46, 132, fanT));
  const angle = Math.PI / 2 + (count === 1 ? 0 : (index / (count - 1) - 0.5) * spread);
  const maxRadius = Math.max(116, height - server.y - 64);
  const radius = clamp(Math.min(width * 0.24, height * 0.42), 116, maxRadius);

  return {
    x: clamp(server.x + Math.cos(angle) * radius, 54, Math.max(54, width - 54)),
    y: clamp(server.y + Math.sin(angle) * radius, server.y + 82, Math.max(server.y + 82, height - 48)),
  };
}

export function overviewRingPosition(
  node: TopologyNode,
  clients: TopologyNode[],
  width: number,
  height: number,
) {
  const centerX = width / 2;
  const radiusY = clamp(Math.min(height * 0.31, width * 0.17), 132, Math.max(132, height * 0.38));
  const centerY = clamp(height * 0.52, radiusY + 78, Math.max(radiusY + 78, height - 70));
  const server = { x: centerX, y: centerY - radiusY };
  if (node.id === SERVER_NODE_ID) {
    return server;
  }

  const index = Math.max(0, clients.findIndex((candidate) => candidate.id === node.id));
  const count = Math.max(1, clients.length);
  const radiusX = clamp(Math.min(width * 0.3, radiusY * 1.9), 190, Math.max(190, width / 2 - 78));
  const denseT = Math.max(0, count - 10) / 14;
  const topGap = lerp(120, 82, denseT);
  const start = -90 + topGap / 2;
  const span = 360 - topGap;
  const angle = radians(start + ((index + 0.5) / count) * span);

  return {
    x: clamp(centerX + Math.cos(angle) * radiusX, 54, Math.max(54, width - 54)),
    y: clamp(centerY + Math.sin(angle) * radiusY, server.y + 78, Math.max(server.y + 78, height - 48)),
  };
}

export function overviewNodePosition(
  node: TopologyNode,
  nodes: TopologyNode[],
  width: number,
  height: number,
) {
  const clients = nodes.filter((candidate) => candidate.kind === 'client');
  if (clients.length <= OVERVIEW_FAN_CLIENT_LIMIT) {
    return overviewFanPosition(node, clients, width, height);
  }
  return overviewRingPosition(node, clients, width, height);
}

export function focusAnchorPosition(width: number, height: number) {
  const activeY = clamp(height * 0.42, 132, Math.max(132, height - 150));
  const serverGap = clamp(height * 0.22, 72, 112);
  return {
    active: {
      x: width / 2,
      y: activeY,
    },
    server: {
      x: width / 2,
      y: clamp(activeY - serverGap, 58, Math.max(58, activeY - 72)),
    },
  };
}

export function focusFanPosition(
  node: TopologyNode,
  peers: TopologyNode[],
  active: { x: number; y: number },
  width: number,
  height: number,
) {
  const index = Math.max(0, peers.findIndex((candidate) => candidate.id === node.id));
  const count = Math.max(1, peers.length);
  const fanT = count <= 2 ? 0 : (count - 2) / (FOCUS_FAN_CLIENT_LIMIT - 2);
  const spread = radians(count === 1 ? 0 : lerp(52, 150, fanT));
  const angle = Math.PI / 2 + (count === 1 ? 0 : (index / (count - 1) - 0.5) * spread);
  const maxRadius = Math.max(96, height - active.y - 54);
  const radius = clamp(Math.min(width * 0.22, height * 0.3), 96, maxRadius);

  return {
    x: clamp(active.x + Math.cos(angle) * radius, 54, Math.max(54, width - 54)),
    y: clamp(active.y + Math.sin(angle) * radius, active.y + 84, Math.max(active.y + 84, height - 48)),
  };
}

export function focusRingPosition(
  node: TopologyNode,
  peers: TopologyNode[],
  active: { x: number; y: number },
  width: number,
  height: number,
) {
  const index = Math.max(0, peers.findIndex((candidate) => candidate.id === node.id));
  const count = Math.max(1, peers.length);
  const radiusY = clamp(Math.min(height * 0.22, width * 0.13), 84, Math.max(84, height - active.y - 56));
  const radiusX = clamp(Math.min(width * 0.28, radiusY * 2.1), 180, Math.max(180, width / 2 - 72));
  const centerY = clamp(active.y + radiusY * 0.95, active.y + 96, Math.max(active.y + 96, height - 52));
  const start = 24;
  const span = 132;
  const angle = radians(start + ((index + 0.5) / count) * span);

  return {
    x: clamp(active.x + Math.cos(angle) * radiusX, 54, Math.max(54, width - 54)),
    y: clamp(centerY + Math.sin(angle) * radiusY, active.y + 84, Math.max(active.y + 84, height - 48)),
  };
}

export function focusNodePosition(
  node: TopologyNode,
  nodes: TopologyNode[],
  pinnedId: string,
  width: number,
  height: number,
) {
  const { active, server } = focusAnchorPosition(width, height);
  if (node.id === pinnedId) {
    return active;
  }
  if (node.id === SERVER_NODE_ID) {
    return server;
  }

  const peers = nodes
    .filter((candidate) => candidate.kind === 'client' && candidate.id !== pinnedId)
    .sort((a, b) => a.id.localeCompare(b.id));
  if (peers.length <= FOCUS_FAN_CLIENT_LIMIT) {
    return focusFanPosition(node, peers, active, width, height);
  }
  return focusRingPosition(node, peers, active, width, height);
}

export function initialNodePosition(
  node: TopologyNode,
  nodes: TopologyNode[],
  pinnedId: string,
  width: number,
  height: number,
) {
  if (pinnedId === SERVER_NODE_ID) {
    return overviewNodePosition(node, nodes, width, height);
  }
  return focusNodePosition(node, nodes, pinnedId, width, height);
}
