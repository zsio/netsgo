import { useState } from 'react';
import toast from 'react-hot-toast';
import { useQueryClient } from '@tanstack/react-query';

import type { Client, ClientBandwidthSettingsResponse } from '@/types';
import { api } from '@/lib/api';
import { bpsToMbpsInput, parseMbpsInputToBps } from '@/lib/format';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';

interface ClientBandwidthDialogProps {
  client: Client;
}

export function ClientBandwidthDialog({ client }: ClientBandwidthDialogProps) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [ingressBps, setIngressBps] = useState('');
  const [egressBps, setEgressBps] = useState('');

  const syncForm = () => {
    setIngressBps(bpsToMbpsInput(client.ingress_bps));
    setEgressBps(bpsToMbpsInput(client.egress_bps));
  };

  const handleOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (nextOpen) {
      syncForm();
    }
  };

  const handleSave = async () => {
    const parsedIngressBps = parseMbpsInputToBps(ingressBps);
    const parsedEgressBps = parseMbpsInputToBps(egressBps);
    if (parsedIngressBps == null || parsedEgressBps == null) {
      toast.error('带宽限制必须是非负数');
      return;
    }

    setIsSaving(true);
    try {
      await api.put<ClientBandwidthSettingsResponse>(
        `/api/clients/${client.id}/bandwidth-settings`,
        {
          ingress_bps: parsedIngressBps,
          egress_bps: parsedEgressBps,
        },
      );
      await queryClient.invalidateQueries({ queryKey: ['clients'] });
      setOpen(false);
      toast.success('客户端带宽设置已保存');
    } catch (error) {
      const message = error instanceof Error ? error.message : '保存失败，请稍后重试';
      toast.error(message);
    } finally {
      setIsSaving(false);
    }
  };

  const parsedIngressBps = parseMbpsInputToBps(ingressBps);
  const parsedEgressBps = parseMbpsInputToBps(egressBps);
  const isValid = parsedIngressBps !== null && parsedEgressBps !== null;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button variant="outline">带宽设置</Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>编辑客户端带宽</DialogTitle>
          <DialogDescription>
            配置当前 Client 的聚合入站与出站限速，单位为 MiB/s。
          </DialogDescription>
        </DialogHeader>

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">入站限速</label>
            <Input
              type="number"
              min={0}
              step="any"
              placeholder="0"
              value={ingressBps}
              onChange={(event) => setIngressBps(event.target.value)}
              disabled={isSaving}
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-sm font-medium">出站限速</label>
            <Input
              type="number"
              min={0}
              step="any"
              placeholder="0"
              value={egressBps}
              onChange={(event) => setEgressBps(event.target.value)}
              disabled={isSaving}
            />
          </div>
        </div>

        <p className="text-[11px] text-muted-foreground">
          留空或填写 0 表示不限速。更新在线 Client 时应立即生效。
        </p>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={isSaving}
          >
            取消
          </Button>
          <Button
            type="button"
            onClick={handleSave}
            disabled={!isValid || isSaving}
          >
            {isSaving ? '保存中…' : '保存修改'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
