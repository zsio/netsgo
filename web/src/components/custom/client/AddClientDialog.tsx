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
  Key, Copy, Check, RefreshCcw, Terminal, Loader2, LayersPlus, Link, Box, FileText,
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
import { cn } from '@/lib/utils';

import { resolveAddClientServiceAddress } from './client-service-address';
import {
  clientCNBDockerImageForVersion,
  clientDockerImageForVersion,
  clientInstallChannelArgForVersion,
  hasKnownClientInstallVersion,
  INSTALL_SCRIPT_URL,
} from './client-install-commands';
import { ShikiCodeBlock } from './ShikiCodeBlock';

const EXPIRY_OPTIONS = [
  { labelKey: 'clients.expiry1h', value: '1h' },
  { labelKey: 'clients.expiry3h', value: '3h' },
  { labelKey: 'clients.expiry1d', value: '24h' },
  { labelKey: 'clients.expiry7d', value: '168h' },
  { labelKey: 'common.unlimited', value: '0' },
] as const;

type CommandTab = 'install' | 'docker' | 'compose' | 'run';
type CopyTarget = 'key' | 'url' | CommandTab;

interface AddClientDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

function shellQuote(value: string) {
  return `'${value.replace(/'/g, `'"'"'`)}'`;
}

function yamlDoubleQuote(value: string) {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
}

async function writeClipboardText(value: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // Try the textarea fallback for HTTP or permission-restricted contexts.
    }
  }

  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand('copy');
  document.body.removeChild(textarea);
}

export function AddClientDialog({ open, onOpenChange }: AddClientDialogProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<'config' | 'result'>('config');
  const [maxUses, setMaxUses] = useState(0);
  const [expiresIn, setExpiresIn] = useState('0');
  const [generatedKey, setGeneratedKey] = useState('');
  const [generatedServerVersion, setGeneratedServerVersion] = useState('');
  const [serverAddr, setServerAddr] = useState('');
  const [activeCommandTab, setActiveCommandTab] = useState<CommandTab>('install');
  const [copied, setCopied] = useState<CopyTarget | null>(null);
  const [isWaitingForStatus, setIsWaitingForStatus] = useState(false);
  const [statusLoadFailed, setStatusLoadFailed] = useState(false);

  const createKey = useCreateAPIKey();
  const { data: adminConfig } = useAdminConfig({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });
  const { data: status, refetch: refetchStatus } = useServerStatus({
    enabled: open,
    refetchOnMount: 'always',
    staleTime: 0,
  });

  const handleReset = useCallback(() => {
    setStep('config');
    setMaxUses(0);
    setExpiresIn('0');
    setGeneratedKey('');
    setGeneratedServerVersion('');
    setServerAddr('');
    setActiveCommandTab('install');
    setCopied(null);
    setIsWaitingForStatus(false);
    setStatusLoadFailed(false);
  }, []);

  const handleOpenChange = useCallback((v: boolean) => {
    if (!v) handleReset();
    onOpenChange(v);
  }, [onOpenChange, handleReset]);

  const handleGenerate = useCallback(async () => {
    if (createKey.isPending || isWaitingForStatus) return;

    setStatusLoadFailed(false);
    let statusForCommand = status;
    if (!hasKnownClientInstallVersion(statusForCommand?.version)) {
      setIsWaitingForStatus(true);
      try {
        const result = await refetchStatus({ cancelRefetch: false });
        statusForCommand = result.data;
      } catch {
        setStatusLoadFailed(true);
        return;
      } finally {
        setIsWaitingForStatus(false);
      }
    }

    const commandVersion = statusForCommand?.version.trim() ?? '';
    if (!hasKnownClientInstallVersion(commandVersion)) {
      setStatusLoadFailed(true);
      return;
    }

    const statusServerAddr = statusForCommand?.server_addr;
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
          setGeneratedServerVersion(commandVersion);
          setServerAddr(resolveAddClientServiceAddress({
            effectiveServerAddr: adminConfig?.effective_server_addr,
            adminServerAddr: adminConfig?.server_addr,
            keyServerAddr: data.server_addr,
            statusServerAddr,
            browserOrigin: window.location.origin,
          }));
          setStep('result');
        },
      },
    );
  }, [adminConfig, createKey, expiresIn, isWaitingForStatus, maxUses, refetchStatus, status]);

  const copyToClipboard = useCallback(async (text: string, tag: CopyTarget) => {
    try {
      await writeClipboardText(text);
      setCopied(tag);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      // fallback
    }
  }, []);

  const connectCmd = generatedKey
    ? [
      'netsgo client \\',
      `  --server ${shellQuote(serverAddr)} \\`,
      `  --key ${shellQuote(generatedKey)}`,
    ].join('\n')
    : '';
  const installChannelArg = clientInstallChannelArgForVersion(generatedServerVersion);
  const installChannelLines = installChannelArg ? [`  ${installChannelArg} \\`] : [];
  const clientDockerImage = clientDockerImageForVersion(generatedServerVersion);
  const clientCNBDockerImage = clientCNBDockerImageForVersion(generatedServerVersion);
  const installAndRunCmd = generatedKey
    ? [
      `curl -fsSL ${INSTALL_SCRIPT_URL} | sh -s -- \\`,
      ...installChannelLines,
      '  --client \\',
      `  --server ${shellQuote(serverAddr)} \\`,
      `  --key ${shellQuote(generatedKey)}`,
    ].join('\n')
    : '';
  const dockerRunCmd = generatedKey
    ? [
      'docker run -d \\',
      '  --name netsgo-client \\',
      '  --restart unless-stopped \\',
      '  --user 0:0 \\',
      '  --network host \\',
      `  -e NETSGO_SERVER=${shellQuote(serverAddr)} \\`,
      `  -e NETSGO_KEY=${shellQuote(generatedKey)} \\`,
      '  -v netsgo-client-data:/var/lib/netsgo \\',
      `  ${clientDockerImage} \\`,
      '  client --data-dir /var/lib/netsgo',
    ].join('\n')
    : '';
  const dockerComposeConfig = generatedKey
    ? [
      'services:',
      '  netsgo-client:',
      `    # ${t('clients.dockerComposeChinaImageComment', { image: clientCNBDockerImage })}`,
      `    image: ${clientDockerImage}`,
      '    restart: unless-stopped',
      '    user: "0:0"',
      '    network_mode: host',
      '    environment:',
      `      NETSGO_SERVER: ${yamlDoubleQuote(serverAddr)}`,
      `      NETSGO_KEY: ${yamlDoubleQuote(generatedKey)}`,
      '    command:',
      '      - client',
      '      - --data-dir',
      '      - /var/lib/netsgo',
      '    volumes:',
      '      - netsgo-client-data:/var/lib/netsgo',
      '',
      'volumes:',
      '  netsgo-client-data:',
    ].join('\n')
    : '';
  const isGeneratePending = createKey.isPending || isWaitingForStatus;
  const commandTabs = [
    { value: 'install' as const, icon: Link, label: t('clients.installAndRun'), language: 'bash' as const },
    { value: 'docker' as const, icon: Box, label: t('clients.dockerRun'), language: 'bash' as const },
    { value: 'compose' as const, icon: FileText, label: t('clients.dockerCompose'), language: 'yaml' as const },
    { value: 'run' as const, icon: Terminal, label: t('clients.runInstalled'), language: 'bash' as const },
  ];
  const commandByTab: Record<CommandTab, string> = {
    install: installAndRunCmd,
    docker: dockerRunCmd,
    compose: dockerComposeConfig,
    run: connectCmd,
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className={cn(
        "max-h-[calc(100vh-2rem)] w-[calc(100vw-1rem)] max-w-[calc(100vw-1rem)] overflow-y-auto",
        step === 'config' ? 'sm:max-w-[400px]' : 'sm:max-w-[640px]',
      )}>
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
              <p className="text-[11px] leading-snug text-muted-foreground">
                {t('clients.expiresInHint')}
              </p>
            </div>

            {/* 生成按钮 */}
            <Button
              className="w-full"
              onClick={handleGenerate}
              disabled={isGeneratePending}
            >
              {isGeneratePending ? (
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

            {(createKey.isError || statusLoadFailed) && (
              <p className="text-xs text-destructive text-center">
                {t('clients.generateFailed')}
              </p>
            )}
          </div>
        )}

        {step === 'result' && (
          <div className="flex min-w-0 flex-col gap-4 pt-1">
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <div className="grid divide-y divide-border">
                <div className="grid grid-cols-[4.5rem_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2.5">
                  <span className="text-xs font-medium text-muted-foreground">
                    {t('clients.connectionURL')}
                  </span>
                  <code className="min-w-0 select-all break-all font-mono text-xs leading-5 text-foreground" title={serverAddr}>
                    {serverAddr}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0 text-muted-foreground"
                    title={t('clients.copyLabel', { label: t('clients.connectionURL') })}
                    aria-label={t('clients.copyLabel', { label: t('clients.connectionURL') })}
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
                  <code className="min-w-0 select-all break-all font-mono text-xs leading-5 text-foreground" title={generatedKey}>
                    {generatedKey}
                  </code>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    className="shrink-0 text-muted-foreground"
                    title={t('clients.copyLabel', { label: t('clients.connectionKey') })}
                    aria-label={t('clients.copyLabel', { label: t('clients.connectionKey') })}
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
              onValueChange={value => setActiveCommandTab(value as CommandTab)}
              className="gap-2"
            >
              <TabsList>
                {commandTabs.map((tab) => {
                  const Icon = tab.icon;
                  return (
                    <TabsTrigger
                      key={tab.value}
                      value={tab.value}
                    >
                      <Icon data-icon="inline-start" className="hidden sm:block" />
                      {tab.label}
                    </TabsTrigger>
                  );
                })}
              </TabsList>
              {commandTabs.map(tab => (
                <TabsContent key={tab.value} value={tab.value} className="mt-0 min-w-0">
                  <div className="relative min-w-0 overflow-hidden rounded-lg border border-border bg-muted/30">
                    <ShikiCodeBlock
                      code={commandByTab[tab.value]}
                      language={tab.language}
                    />
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="absolute right-2 top-2 text-muted-foreground"
                      title={t('clients.copyLabel', { label: tab.label })}
                      aria-label={t('clients.copyLabel', { label: tab.label })}
                      onClick={() => copyToClipboard(commandByTab[tab.value], tab.value)}
                    >
                      {copied === tab.value ? (
                        <Check className="text-primary" />
                      ) : (
                        <Copy />
                      )}
                    </Button>
                  </div>
                </TabsContent>
              ))}
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
