import { ArrowRightLeft } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TunnelListTable, type TunnelEntry } from '@/components/custom/tunnel/TunnelListTable';
import type { Client } from '@/types';

interface TunnelTableProps {
  client: Client;
}

export function TunnelTable({ client }: TunnelTableProps) {
  const tunnels: TunnelEntry[] = (client.proxies ?? []).map((proxy) => ({
    ...proxy,
    clientId: client.id,
    clientName: client.info.hostname,
  }));

  return (
    <TunnelListTable
      tunnels={tunnels}
      title="下属隧道"
      icon={<ArrowRightLeft className="h-5 w-5 text-primary" />}
      showClient={false}
      showActions
      showSearch
      emptyAction={
        <Button variant="outline" className="mt-4">
          + 立即创建
        </Button>
      }
    />
  );
}
