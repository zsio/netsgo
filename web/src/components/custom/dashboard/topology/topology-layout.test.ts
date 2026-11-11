import { describe, expect, test } from 'vitest';

import { SERVER_NODE_ID, type TopologyNode } from './topology-model';
import {
  FOCUS_FAN_CLIENT_LIMIT,
  OVERVIEW_FAN_CLIENT_LIMIT,
  focusAnchorPosition,
  initialNodePosition,
  overviewServerPosition,
} from './topology-layout';

function serverNode(): TopologyNode {
  return {
    id: SERVER_NODE_ID,
    kind: 'server',
    label: 'NetsGo Server',
    online: true,
    client: null,
  };
}

function clientNode(id: string): TopologyNode {
  return {
    id,
    kind: 'client',
    label: id,
    online: true,
    client: null,
  };
}

describe('topology-layout', () => {
  test('places server at top center and fans out small client sets in overview mode', () => {
    const width = 960;
    const height = 460;
    const clients = [clientNode('c-1'), clientNode('c-2'), clientNode('c-3')];
    const nodes = [serverNode(), ...clients];

    const serverPos = initialNodePosition(nodes[0], nodes, SERVER_NODE_ID, width, height);
    expect(serverPos).toEqual(overviewServerPosition(width, height));
    expect(serverPos.x).toBe(width / 2);

    const p1 = initialNodePosition(clients[0], nodes, SERVER_NODE_ID, width, height);
    const p2 = initialNodePosition(clients[1], nodes, SERVER_NODE_ID, width, height);
    const p3 = initialNodePosition(clients[2], nodes, SERVER_NODE_ID, width, height);

    expect(p2.x).toBeCloseTo(width / 2, 5);
    expect(p1.x).toBeGreaterThan(p3.x);
    for (const pos of [p1, p2, p3]) {
      expect(pos.x).toBeGreaterThanOrEqual(54);
      expect(pos.x).toBeLessThanOrEqual(width - 54);
      expect(pos.y).toBeGreaterThan(serverPos.y);
      expect(pos.y).toBeLessThanOrEqual(height - 48);
    }
  });

  test('switches to ring layout for dense client sets in overview mode while staying within bounds', () => {
    const width = 960;
    const height = 460;
    const clients = Array.from({ length: OVERVIEW_FAN_CLIENT_LIMIT + 5 }, (_, idx) =>
      clientNode(`c-${idx + 1}`),
    );
    const nodes = [serverNode(), ...clients];
    const serverPos = initialNodePosition(nodes[0], nodes, SERVER_NODE_ID, width, height);

    for (const client of clients) {
      const pos = initialNodePosition(client, nodes, SERVER_NODE_ID, width, height);
      expect(pos.x).toBeGreaterThanOrEqual(54);
      expect(pos.x).toBeLessThanOrEqual(width - 54);
      expect(pos.y).toBeGreaterThanOrEqual(serverPos.y + 78);
      expect(pos.y).toBeLessThanOrEqual(height - 48);
    }
  });

  test('anchors focused client below server and arranges peers below active node in both fan and ring modes', () => {
    const width = 960;
    const height = 460;
    const peers = Array.from({ length: FOCUS_FAN_CLIENT_LIMIT + 3 }, (_, idx) =>
      clientNode(`peer-${String(idx + 1).padStart(2, '0')}`),
    );
    const focused = clientNode('focused-node');
    const nodes = [serverNode(), focused, ...peers];

    const anchors = focusAnchorPosition(width, height);
    expect(initialNodePosition(focused, nodes, focused.id, width, height)).toEqual(anchors.active);
    expect(initialNodePosition(nodes[0], nodes, focused.id, width, height)).toEqual(anchors.server);
    expect(anchors.server.y).toBeLessThan(anchors.active.y);

    for (const peer of peers) {
      const pos = initialNodePosition(peer, nodes, focused.id, width, height);
      expect(pos.x).toBeGreaterThanOrEqual(54);
      expect(pos.x).toBeLessThanOrEqual(width - 54);
      expect(pos.y).toBeGreaterThan(anchors.active.y);
      expect(pos.y).toBeLessThanOrEqual(height - 48);
    }
  });
});
