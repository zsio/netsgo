import { useState, useCallback } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Key, Copy, Check, RefreshCcw, Terminal, Loader2, LayersPlus,
} from 'lucide-react';
import { useAdminConfig } from '@/hooks/use-admin-config';
import { useCreateAPIKey } from '@/hooks/use-admin-keys';
import { useServerStatus } from '@/hooks/use-server-status';
import { useTranslation } from 'react-i18next';

import { resolveAddClientServiceAddress } from './client-service-address';

const EXPIRY_OPTIONS = [
  { labelKey: 'clients.expiry1h', value: '1h' },
  { labelKey: 'clients.expiry3h', value: '3h' },
  { labelKey: 'clients.expiry1d', value: '24h' },
  { labelKey: 'clients.expiry7d', value: '168h' },
  { labelKey: 'common.unlimited', value: '0' },
] as const;

interface AddClientDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function AddClientDialog({ open, onOpenChange }: AddClientDialogProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<'config' | 'result'>('config');
  const [maxUses, setMaxUses] = useState(0);
  const [expiresIn, setExpiresIn] = useState('0');
  const [generatedKey, setGeneratedKey] = useState('');
  const [serverAddr, setServerAddr] = useState('');
  const [copied, setCopied] = useState<'key' | 'cmd' | null>(null);

  const createKey = useCreateAPIKey();
  const { data: adminConfig } = useAdminConfig({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });
  const { data: status } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const handleReset = useCallback(() => {
    setStep('config');
    setMaxUses(0);
    setExpiresIn('0');
    setGeneratedKey('');
    setServerAddr('');
    setCopied(null);
  }, []);

  const handleOpenChange = useCallback((v: boolean) => {
    if (!v) handleReset();
    onOpenChange(v);
  }, [onOpenChange, handleReset]);

  const handleGenerate = useCallback(() => {
    const name = `quick-${Date.now().toString(36)}`;
    createKey.mutate(
      {
        name,
        permissions: ['connect'],
        max_uses: maxUses,
        expires_in: expiresIn,
      },
      {
        onSuccess: (data) => {
          setGeneratedKey(data.raw_key);
          setServerAddr(resolveAddClientServiceAddress({
            effectiveServerAddr: adminConfig?.effective_server_addr,
            adminServerAddr: adminConfig?.server_addr,
            keyServerAddr: data.server_addr,
            statusServerAddr: status?.server_addr,
            browserOrigin: window.location.origin,
          }));
          setStep('result');
        },
      },
    );
  }, [adminConfig, createKey, expiresIn, maxUses, status]);

  const copyToClipboard = useCallback(async (text: string, tag: 'key' | 'cmd') => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(tag);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      // fallback
    }
  }, []);

  const connectCmd = generatedKey
    ? `netsgo client --server ${serverAddr} --key ${generatedKey}`
    : '';

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <LayersPlus className="h-5 w-5 text-primary" />
            {t('clients.addTitle')}
          </DialogTitle>
          <DialogDescription>
            {t('clients.addDescription')}
          </DialogDescription>
        </DialogHeader>

        {step === 'config' && (
          <div className="space-y-5 pt-1">
            <div className="space-y-2">
              <label className="text-sm font-medium text-foreground">
                {t('clients.maxUses')}
              </label>
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min={0}
                  value={maxUses}
                  onChange={e => setMaxUses(Math.max(0, parseInt(e.target.value) || 0))}
                  className="w-28 text-center"
                  placeholder="0"
                />
                <span className="text-xs text-muted-foreground">
                  {maxUses === 0 ? t('clients.maxUsesUnlimited') : t('clients.maxUsesCount', { count: maxUses })}
                </span>
              </div>
            </div>

            <div className="space-y-2">
              <label className="text-sm font-medium text-foreground">
                {t('clients.expiresIn')}
              </label>
              <div className="flex flex-wrap gap-2">
                {EXPIRY_OPTIONS.map(opt => (
                  <button
                    key={opt.value}
                    type="button"
                    onClick={() => setExpiresIn(opt.value)}
                    className={`px-3 py-1.5 text-xs rounded-lg border transition-all duration-150 cursor-pointer ${
                      expiresIn === opt.value
                        ? 'bg-primary text-primary-foreground border-primary shadow-sm'
                        : 'bg-muted/50 text-muted-foreground border-border hover:bg-muted hover:text-foreground'
                    }`}
                  >
                    {t(opt.labelKey)}
                  </button>
                ))}
              </div>
            </div>

            {/* 生成按钮 */}
            <Button
              className="w-full"
              onClick={handleGenerate}
              disabled={createKey.isPending}
            >
              {createKey.isPending ? (
                <>
                  <Loader2 className="h-4 w-4 mr-2 animate-spin" />
                  {t('clients.generating')}
                </>
              ) : (
                <>
                  <Key className="h-4 w-4 mr-2" />
                  {t('clients.generateKey')}
                </>
              )}
            </Button>

            {createKey.isError && (
              <p className="text-xs text-destructive text-center">
                {t('clients.generateFailed')}
              </p>
            )}
          </div>
        )}

        {step === 'result' && (
          <div className="space-y-4 pt-1">
            {/* 生成的 Key */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                {t('clients.connectionKey')}
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                  {generatedKey}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="shrink-0"
                  onClick={() => copyToClipboard(generatedKey, 'key')}
                >
                  {copied === 'key' ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
              <p className="text-[10px] text-muted-foreground">
                {t('clients.keyOneTime')}
              </p>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
                {t('clients.recommendedServiceAddress')}
              </label>
              <code className="block px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                {serverAddr}
              </code>
              <p className="text-[11px] text-muted-foreground">
                {t('clients.serviceAddressHelp')}
              </p>
            </div>

            {adminConfig?.server_addr_locked && (
              <div className="rounded-lg border border-amber-500/25 bg-amber-500/8 p-3 text-[11px] text-muted-foreground">
                {t('clients.serverAddrLocked')}
                <div className="mt-1 font-mono text-foreground break-all">
                  {t('clients.effectiveServiceAddress', { address: adminConfig.effective_server_addr })}
                </div>
              </div>
            )}

            {/* 连接命令 */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground uppercase tracking-wider flex items-center gap-1.5">
                <Terminal className="h-3.5 w-3.5" />
                {t('clients.connectionCommand')}
              </label>
              <div className="flex items-center gap-2">
                <code className="flex-1 px-3 py-2 text-xs font-mono bg-muted rounded-lg border border-border break-all select-all">
                  {connectCmd}
                </code>
                <Button
                  variant="ghost"
                  size="icon"
                  className="shrink-0"
                  onClick={() => copyToClipboard(connectCmd, 'cmd')}
                >
                  {copied === 'cmd' ? (
                    <Check className="h-4 w-4 text-green-500" />
                  ) : (
                    <Copy className="h-4 w-4" />
                  )}
                </Button>
              </div>
              <p className="text-[11px] text-muted-foreground">
                {t('clients.commandHelp')}
              </p>
            </div>

            {/* 操作按钮 */}
            <div className="flex gap-2 pt-1">
              <Button
                variant="outline"
                className="flex-1"
                onClick={handleReset}
              >
                <RefreshCcw className="h-4 w-4 mr-2" />
                {t('clients.regenerate')}
              </Button>
              <Button
                variant="default"
                className="flex-1"
                onClick={() => handleOpenChange(false)}
              >
                {t('clients.done')}
              </Button>
            </div>
          </div>
        )}

        {/* 使用说明 */}
        <div className="mt-2 rounded-lg bg-muted/50 border border-border/50 p-3 space-y-2">
          <p className="text-xs font-medium text-foreground">{t('clients.instructions')}</p>
          <ol className="text-[11px] text-muted-foreground space-y-1.5 list-decimal list-inside">
            <li>{t('clients.instructionInstall')}</li>
            <li>{t('clients.instructionRun')}</li>
            <li>{t('clients.instructionReplaceServer')}</li>
            <li>{t('clients.instructionVisible')}</li>
          </ol>
        </div>
      </DialogContent>
    </Dialog>
  );
}
