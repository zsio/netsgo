import { useState } from 'react';
import toast from 'react-hot-toast';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';

import type { ReactNode } from 'react';
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
import { FoldVertical } from 'lucide-react';

interface ClientBandwidthDialogProps {
  client: Client;
  trigger?: ReactNode;
}

export function ClientBandwidthDialog({ client, trigger }: ClientBandwidthDialogProps) {
  const { t } = useTranslation();
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
      toast.error(t('tunnels.bandwidthNonNegative'));
      return;
    }

    setIsSaving(true);
    try {
      await api.put<ClientBandwidthSettingsResponse>(
        `/api/clients/${encodeURIComponent(client.id)}/bandwidth-settings`,
        {
          ingress_bps: parsedIngressBps,
          egress_bps: parsedEgressBps,
        },
      );
      await queryClient.invalidateQueries({ queryKey: ['clients'] });
      setOpen(false);
      toast.success(t('tunnels.bandwidthSaved'));
    } catch (error) {
      const message = error instanceof Error ? error.message : t('tunnels.bandwidthSaveFailed');
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
        {trigger || (
          <Button variant="outline">
            <FoldVertical className="h-4 w-4 mr-1.5" />
            {t('tunnels.bandwidthLimit')}
          </Button>
        )}
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('clients.editBandwidth')}</DialogTitle>
          <DialogDescription>
            {t('tunnels.editClientBandwidthDescription')}
          </DialogDescription>
        </DialogHeader>

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label className="text-sm font-medium">{t('tunnels.ingressLimit')}</label>
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
            <label className="text-sm font-medium">{t('tunnels.egressLimit')}</label>
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
          {t('tunnels.bandwidthImmediateHelp')}
        </p>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => setOpen(false)}
            disabled={isSaving}
          >
            {t('common.cancel')}
          </Button>
          <Button
            type="button"
            onClick={handleSave}
            disabled={!isValid || isSaving}
          >
            {isSaving ? t('common.saving') : t('tunnels.saveChanges')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
