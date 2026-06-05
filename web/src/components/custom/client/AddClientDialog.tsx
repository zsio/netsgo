import { useState, useCallback } from 'react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Key, Copy, Check, RefreshCcw, Terminal, Loader2, LayersPlus, Link,
} from 'lucide-react';
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui/tabs';
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

const INSTALL_SCRIPT_URL = 'https://netsgo.zs.uy/install.sh';

interface AddClientDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'"'"'`)}'`;
}

export function AddClientDialog({ open, onOpenChange }: AddClientDialogProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<'config' | 'result'>('config');
  const [maxUses, setMaxUses] = useState(0);
  const [expiresIn, setExpiresIn] = useState('0');
  const [generatedKey, setGeneratedKey] = useState('');
  const [serverAddr, setServerAddr] = useState('');
  const [activeCommandTab, setActiveCommandTab] = useState<'install' | 'run'>('install');
  const [copied, setCopied] = useState<'key' | 'url' | 'cmd' | null>(null);

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
    setActiveCommandTab('install');
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

  const copyToClipboard = useCallback(async (text: string, tag: 'key' | 'url' | 'cmd') => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(tag);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      // fallback
    }
  }, []);

  const connectCmd = generatedKey
    ? `netsgo client --server ${shellQuote(serverAddr)} --key ${shellQuote(generatedKey)}`
    : '';
  const installAndRunCmd = connectCmd
    ? `curl -fsSL ${INSTALL_SCRIPT_URL} | sh -s -- --client --server ${shellQuote(serverAddr)} --key ${shellQuote(generatedKey)}`
    : '';

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <LayersPlus className="h-5 w-5 text-primary" />
            {t('clients.addTitle')}
          </DialogTitle>
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
          <div className="flex flex-col gap-4 pt-1">
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <div className="grid divide-y divide-border">
                <div className="grid grid-cols-[4.5rem_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2.5">
                  <span className="text-xs font-medium text-muted-foreground">
                    {t('clients.connectionURL')}
                  </span>
                  <code className="min-w-0 truncate text-xs font-mono text-foreground" title={serverAddr}>
                    {serverAddr}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0 text-muted-foreground"
                    onClick={() => copyToClipboard(serverAddr, 'url')}
                  >
                    {copied === 'url' ? (
                      <Check className="text-primary" />
                    ) : (
                      <Copy />
                    )}
                  </Button>
                </div>

                <div className="grid grid-cols-[4.5rem_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2.5">
                  <span className="text-xs font-medium text-muted-foreground">
                    {t('clients.connectionKey')}
                  </span>
                  <code className="min-w-0 truncate text-xs font-mono text-foreground" title={generatedKey}>
                    {generatedKey}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0 text-muted-foreground"
                    onClick={() => copyToClipboard(generatedKey, 'key')}
                  >
                    {copied === 'key' ? (
                      <Check className="text-primary" />
                    ) : (
                      <Copy />
                    )}
                  </Button>
                </div>
              </div>
            </div>

            <Tabs
              value={activeCommandTab}
              onValueChange={value => setActiveCommandTab(value as 'install' | 'run')}
              className="gap-2"
            >
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="install">
                  <Link data-icon="inline-start" />
                  {t('clients.installAndRun')}
                </TabsTrigger>
                <TabsTrigger value="run">
                  <Terminal data-icon="inline-start" />
                  {t('clients.runInstalled')}
                </TabsTrigger>
              </TabsList>
              <TabsContent value="install" className="mt-0">
                <div className="relative rounded-lg border border-border bg-muted/40 p-3">
                  <code className="block max-h-32 min-h-16 overflow-auto whitespace-pre-wrap pr-10 text-xs leading-5 font-mono break-all text-foreground select-all">
                    {installAndRunCmd}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="absolute right-2 top-2 text-muted-foreground"
                    onClick={() => copyToClipboard(installAndRunCmd, 'cmd')}
                  >
                    {copied === 'cmd' ? (
                      <Check className="text-primary" />
                    ) : (
                      <Copy />
                    )}
                  </Button>
                </div>
              </TabsContent>
              <TabsContent value="run" className="mt-0">
                <div className="relative rounded-lg border border-border bg-muted/40 p-3">
                  <code className="block max-h-32 min-h-16 overflow-auto whitespace-pre-wrap pr-10 text-xs leading-5 font-mono break-all text-foreground select-all">
                    {connectCmd}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="absolute right-2 top-2 text-muted-foreground"
                    onClick={() => copyToClipboard(connectCmd, 'cmd')}
                  >
                    {copied === 'cmd' ? (
                      <Check className="text-primary" />
                    ) : (
                      <Copy />
                    )}
                  </Button>
                </div>
              </TabsContent>
            </Tabs>

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
      </DialogContent>
    </Dialog>
  );
}
