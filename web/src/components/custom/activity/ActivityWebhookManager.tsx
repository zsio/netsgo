import { useMemo, useRef, useState } from 'react';
import type * as React from 'react';
import type { TFunction } from 'i18next';
import { motion } from 'motion/react';
import toast from 'react-hot-toast';
import {
  AlignLeft,
  Braces,
  Check,
  CircleHelp,
  CircleOff,
  Clock3,
  Copy,
  FileJson2,
  Plus,
  RotateCw,
  Search,
  Send,
  Server,
  Trash2,
  Waypoints,
  Webhook,
  X,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty';
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from '@/components/ui/input-group';
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { ToggleGroup, ToggleGroupItem } from '@/components/custom/toggle-group';
import { cn, copyText } from '@/lib/utils';
import { WebhookJsonEditor, type WebhookJsonEditorHandle } from '@/components/custom/webhooks/WebhookJsonEditor';
import {
  getPickerVariables,
  getTemplateIssues,
  type PickerVariable,
  renderWebhookRequest,
  validateWebhook,
  webhookVariableSample,
  type WebhookValidationIssue,
} from '@/components/custom/webhooks/webhook-template';
import { useClients } from '@/hooks/use-clients';
import {
  useDeleteWebhook,
  useReplayWebhookDelivery,
  useSaveWebhook,
  useWebhookCatalog,
  useWebhookDelivery,
  useWebhookDeliveries,
  useWebhooks,
  useTestWebhook,
} from '@/hooks/use-webhooks';
import { SELF_RESOURCE_SCOPE } from '@/lib/resource-scope';
import {
  createEmptyWebhook,
  type ActivityWebhookConfig,
  type WebhookCatalog,
  type WebhookEventFamily,
  type WebhookEventKey,
  type WebhookInvocation,
  type WebhookInvocationStatus,
  type WebhookMethod,
  type WebhookTargetKind,
  type WebhookTargetOption,
  type WebhookTemplateSurface,
} from '@/types/webhook';

type WebhookPrototype = ActivityWebhookConfig;

function eventLabelKey(eventKey: WebhookEventKey) {
  return `webhooks.events.${eventKey}` as const;
}

function variableLabelKey(key: string) {
  return `webhooks.variables.item.${key}` as const;
}

function resolvedPickerLanguage(language: string | undefined, locales: string[]) {
  if (locales.length === 0) return '';
  if (language && locales.includes(language)) return language;
  const primary = language?.split('-')[0]?.toLowerCase();
  return locales.find((locale) => locale.split('-')[0].toLowerCase() === primary) ?? locales[0];
}

function cloneWebhook(webhook: WebhookPrototype) {
  return structuredClone(webhook);
}

function sameWebhook(left: WebhookPrototype | null, right: WebhookPrototype | null) {
  if (!left || !right) return left === right;
  return JSON.stringify({
    name: left.name,
    enabled: left.enabled,
    targetKind: left.targetKind,
    targetMode: left.targetMode,
    targetIds: left.targetIds,
    method: left.method,
    url: left.url,
    headers: left.headers,
    body: left.body,
    events: left.events,
    minIntervalSeconds: left.minIntervalSeconds,
  }) === JSON.stringify({
    name: right.name,
    enabled: right.enabled,
    targetKind: right.targetKind,
    targetMode: right.targetMode,
    targetIds: right.targetIds,
    method: right.method,
    url: right.url,
    headers: right.headers,
    body: right.body,
    events: right.events,
    minIntervalSeconds: right.minIntervalSeconds,
  });
}

function formatShortTime(value: string | null, locale: string) {
  if (!value) return null;
  return new Intl.DateTimeFormat(locale, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value));
}

function validationIssueMessage(t: TFunction, issue: WebhookValidationIssue) {
  return t(`webhooks.validation.${issue.code}`, { key: issue.key ?? '' });
}

function statusVariant(status: WebhookInvocationStatus | WebhookPrototype['lastStatus']) {
  if (status === 'failed') return 'destructive' as const;
  if (status === 'success') return 'secondary' as const;
  return 'outline' as const;
}

export function ActivityWebhookManager({
  open,
  onOpenChange,
  editWebhook,
  showAdminScopeNote = false,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editWebhook: ActivityWebhookConfig | 'new' | null;
  showAdminScopeNote?: boolean;
}) {
  const { t } = useTranslation();
  const { data: catalog } = useWebhookCatalog();
  const { data: webhooks = [] } = useWebhooks();
  const { data: clients = [] } = useClients(SELF_RESOURCE_SCOPE);
  const saveWebhook = useSaveWebhook();
  const removeWebhook = useDeleteWebhook();
  const replayDelivery = useReplayWebhookDelivery();
  // The caller remounts this editor with a fresh key per open, so the draft is
  // initialized once from props here instead of being synced in an effect.
  const [draftOverride, setDraft] = useState<WebhookPrototype | null>(() => {
    if (editWebhook === 'new') return catalog ? createEmptyWebhook(catalog) : null;
    return editWebhook ? cloneWebhook(editWebhook) : null;
  });
  const [activeTab, setActiveTab] = useState<'configuration' | 'deliveries'>('configuration');
  const [pendingAction, setPendingAction] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [testOpen, setTestOpen] = useState(false);
  const [validationIssues, setValidationIssues] = useState<WebhookValidationIssue[]>([]);
  const configurationScrollRef = useRef<HTMLDivElement>(null);

  const draft = draftOverride;
  const saved = draft ? webhooks.find((item) => item.id === draft.id) ?? null : null;
  const isNew = Boolean(draft && !saved);
  const dirty = !sameWebhook(draft, saved);
  const [deliveryStatus, setDeliveryStatus] = useState<'all' | WebhookInvocationStatus>('all');
  const deliveriesQuery = useWebhookDeliveries(saved?.id ?? null, deliveryStatus === 'all' ? undefined : deliveryStatus);
  const invocations = useMemo(
    () => deliveriesQuery.data?.pages.flatMap((page) => page.items) ?? [],
    [deliveriesQuery.data],
  );
  const clientTargets = useMemo<WebhookTargetOption[]>(() => clients.map((client) => {
    const name = client.display_name?.trim() || client.info.hostname || client.id;
    return {
      id: client.id,
      name,
      detail: `${client.info.hostname || client.id} · ${client.online ? 'online' : 'offline'}`,
    };
  }), [clients]);
  const tunnelTargets = useMemo<WebhookTargetOption[]>(() => {
    const targets = new Map<string, WebhookTargetOption>();
    for (const client of clients) {
      const clientName = client.display_name?.trim() || client.info.hostname || client.id;
      for (const tunnel of client.proxies ?? []) {
        if (!targets.has(tunnel.id)) {
          targets.set(tunnel.id, {
            id: tunnel.id,
            name: tunnel.name,
            detail: `${tunnel.type.toUpperCase()} · ${clientName}`,
          });
        }
      }
    }
    return [...targets.values()];
  }, [clients]);

  const updateDraft = <Key extends keyof WebhookPrototype>(key: Key, value: WebhookPrototype[Key]) => {
    setDraft((current) => {
      const base = current ?? draft;
      return base ? { ...base, [key]: value } : base;
    });
    setValidationIssues([]);
  };

  const revealValidationIssue = (issue: WebhookValidationIssue) => {
    setActiveTab('configuration');
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => {
      const field = configurationScrollRef.current?.querySelector<HTMLElement>(`[data-webhook-field="${issue.field}"]`);
      field?.scrollIntoView({ block: 'center' });
      field?.querySelector<HTMLElement>('input, textarea, button, [role="textbox"], [role="checkbox"]')?.focus({ preventScroll: true });
    }));
  };

  const requestClose = () => {
    if (dirty) {
      setPendingAction(true);
      return;
    }
    onOpenChange(false);
  };

  const confirmDiscard = () => {
    setPendingAction(false);
    onOpenChange(false);
  };

  const persistDraft = async (enable = false) => {
    if (!draft || !catalog) return false;
    const candidate = enable ? { ...draft, enabled: true } : draft;
    const issues = validateWebhook(candidate, catalog);
    setValidationIssues(issues);
    if (issues.length > 0) {
      toast.error(validationIssueMessage(t, issues[0]));
      revealValidationIssue(issues[0]);
      return false;
    }
    try {
      const stored = await saveWebhook.mutateAsync(candidate);
      setDraft(cloneWebhook(stored));
      toast.success(isNew ? t('webhooks.toast.created') : t('webhooks.toast.saved'));
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('webhooks.toast.operationFailed'));
      return false;
    }
  };

  const cancelChanges = () => {
    setDraft(saved ? cloneWebhook(saved) : null);
    setValidationIssues([]);
  };

  const requestTest = () => {
    if (!draft || !catalog) return;
    const issues = validateWebhook(draft, catalog);
    setValidationIssues(issues);
    if (issues.length > 0) {
      toast.error(validationIssueMessage(t, issues[0]));
      revealValidationIssue(issues[0]);
      return;
    }
    setTestOpen(true);
  };

  const deleteWebhook = async () => {
    if (!draft) return;
    if (isNew) {
      cancelChanges();
      setDeleteOpen(false);
      return;
    }
    try {
      await removeWebhook.mutateAsync(draft.id);
      setDraft(null);
      setValidationIssues([]);
      setDeleteOpen(false);
      onOpenChange(false);
      toast.success(t('webhooks.toast.deleted'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('webhooks.toast.operationFailed'));
    }
  };

  const replayInvocation = async (invocation: WebhookInvocation) => {
    const configuration = webhooks.find((item) => item.id === invocation.webhookId);
    if (!configuration) {
      toast.error(t('webhooks.toast.replayUnavailable'));
      return;
    }
    try {
      await replayDelivery.mutateAsync(invocation.id);
      toast.success(t('webhooks.toast.replayed'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('webhooks.toast.replayUnavailable'));
    }
  };

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      onOpenChange(true);
      return;
    }
    requestClose();
  };

  return (
    <>
      <Sheet open={open} onOpenChange={handleOpenChange}>
        <SheetContent className="w-full gap-0 data-[side=right]:w-full data-[side=right]:sm:max-w-4xl">
          <SheetHeader className="border-b pr-14">
            <SheetTitle>{t('webhooks.manager.title')}</SheetTitle>
            <SheetDescription className="sr-only">
              {t('webhooks.manager.description')} {showAdminScopeNote ? t('webhooks.manager.adminScope') : t('webhooks.manager.ownScope')}
            </SheetDescription>
          </SheetHeader>

          <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
            <div className="grid min-h-0 flex-1 grid-cols-1 overflow-hidden">
              <main className="min-h-0 min-w-0 overflow-hidden">
              {draft && catalog ? (
                <div className="flex h-full min-h-0 flex-col">
                  <div className="shrink-0 border-b bg-background px-4 py-2.5">
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <div className="truncate font-medium">{draft.name || t('webhooks.manager.webhookFallback')}</div>
                          {isNew ? <Badge variant="secondary">{t('webhooks.status.draft')}</Badge> : null}
                          {dirty ? <Badge variant="outline">{t('webhooks.status.unsaved')}</Badge> : null}
                        </div>
                      </div>
                      <Button variant="ghost" size="icon-sm" onClick={() => setDeleteOpen(true)} aria-label={t('webhooks.editor.delete')} title={t('webhooks.editor.delete')}>
                        <Trash2 />
                      </Button>
                    </div>
                  </div>

                  <Tabs value={activeTab} onValueChange={(value) => setActiveTab(value as typeof activeTab)} className="flex min-h-0 flex-1 flex-col gap-0">
                    <div className="shrink-0 border-b px-4 pt-2">
                      <TabsList variant="line">
                        <TabsTrigger value="configuration">{t('webhooks.manager.configuration')}</TabsTrigger>
                        <TabsTrigger value="deliveries">
                          {t('webhooks.manager.deliveries')}
                          <Badge variant="secondary" className="ml-1">{invocations.filter((item) => item.webhookId === draft.id).length}</Badge>
                        </TabsTrigger>
                      </TabsList>
                    </div>
                    <TabsContent ref={configurationScrollRef} value="configuration" className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-5">
                      <WebhookConfiguration
                        key={draft.id}
                        webhook={draft}
                        catalog={catalog}
                        clientTargets={clientTargets}
                        tunnelTargets={tunnelTargets}
                        isNew={isNew}
                        validationIssues={validationIssues}
                        onUpdate={updateDraft}
                        onTest={requestTest}
                      />
                    </TabsContent>
                    <TabsContent value="deliveries" className="min-h-0 flex-1 overflow-y-auto p-4 sm:p-5">
                      <WebhookDeliveryLog
                        webhookId={draft.id}
                        invocations={invocations}
                        status={deliveryStatus}
                        onStatusChange={setDeliveryStatus}
                        hasMore={Boolean(deliveriesQuery.hasNextPage)}
                        loadingMore={deliveriesQuery.isFetchingNextPage}
                        onLoadMore={() => void deliveriesQuery.fetchNextPage()}
                        onReplay={replayInvocation}
                      />
                    </TabsContent>
                  </Tabs>

                  {activeTab === 'configuration' ? (
                    <footer className="flex shrink-0 flex-col gap-2 border-t bg-background px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                      <div className="text-xs text-muted-foreground">{dirty ? t('webhooks.editor.unsavedHint') : null}</div>
                      <div className="flex flex-wrap items-center justify-end gap-2">
                        <Button variant="ghost" size="sm" onClick={cancelChanges} disabled={!dirty}>
                          <X data-icon="inline-start" />
                          {t('common.cancel')}
                        </Button>
                        <Button
                          variant={isNew && !draft.enabled ? 'outline' : 'default'}
                          size="sm"
                          onClick={() => void persistDraft(false)}
                          disabled={!dirty || saveWebhook.isPending}
                        >
                          <Check data-icon="inline-start" />
                          {isNew && !draft.enabled ? t('webhooks.editor.saveDraft') : t('webhooks.editor.save')}
                        </Button>
                        {isNew && !draft.enabled ? (
                          <Button size="sm" onClick={() => void persistDraft(true)} disabled={saveWebhook.isPending}>
                            <Check data-icon="inline-start" />
                            {t('webhooks.editor.saveAndEnable')}
                          </Button>
                        ) : null}
                      </div>
                    </footer>
                  ) : null}
                </div>
              ) : (
                <Empty className="min-h-80">
                  <EmptyHeader>
                    <EmptyMedia variant="icon"><Webhook /></EmptyMedia>
                    <EmptyTitle>{catalog ? t('webhooks.manager.emptySelection') : t('common.loading')}</EmptyTitle>
                    <EmptyDescription>{catalog ? t('webhooks.manager.emptyDescription') : null}</EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
              </main>
            </div>
          </div>
        </SheetContent>
      </Sheet>

      <AlertDialog open={pendingAction} onOpenChange={(nextOpen) => !nextOpen && setPendingAction(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('webhooks.unsaved.title')}</AlertDialogTitle>
            <AlertDialogDescription>{t('webhooks.unsaved.description')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('webhooks.unsaved.keepEditing')}</AlertDialogCancel>
            <AlertDialogAction onClick={confirmDiscard}>{t('webhooks.unsaved.discard')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{isNew ? t('webhooks.delete.discardTitle') : t('webhooks.delete.title')}</AlertDialogTitle>
            <AlertDialogDescription>{isNew ? t('webhooks.delete.discardDescription') : t('webhooks.delete.description')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void deleteWebhook()} disabled={removeWebhook.isPending}>{isNew ? t('webhooks.delete.discard') : t('webhooks.delete.confirm')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {draft && catalog && testOpen ? (
        <TestWebhookDialog
          key={`${draft.id}-open`}
          open={testOpen}
          webhook={draft}
          catalog={catalog}
          onOpenChange={setTestOpen}
        />
      ) : null}
    </>
  );
}

function WebhookConfiguration({
  webhook,
  catalog,
  clientTargets,
  tunnelTargets,
  isNew,
  validationIssues,
  onUpdate,
  onTest,
}: {
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  clientTargets: WebhookTargetOption[];
  tunnelTargets: WebhookTargetOption[];
  isNew: boolean;
  validationIssues: WebhookValidationIssue[];
  onUpdate: <Key extends keyof WebhookPrototype>(key: Key, value: WebhookPrototype[Key]) => void;
  onTest: () => void;
}) {
  const { t } = useTranslation();
  const fallbackEvent = webhook.targetKind === 'client' ? 'client.online' : 'tunnel.runtime_changed';
  const [sampleEvent, setSampleEvent] = useState<WebhookEventKey>(webhook.events[0] ?? fallbackEvent);
  const [pendingTargetKind, setPendingTargetKind] = useState<WebhookTargetKind | null>(null);
  const [disableOpen, setDisableOpen] = useState(false);
  const effectiveSampleEvent = webhook.events.includes(sampleEvent) ? sampleEvent : webhook.events[0] ?? fallbackEvent;
  const liveBodyIssue = validateWebhook(webhook, catalog).find((issue) => issue.field === 'body');
  const nameIssue = validationIssues.find((issue) => issue.field === 'name');
  const eventIssue = validationIssues.find((issue) => issue.field === 'events');
  const intervalIssue = validationIssues.find((issue) => issue.field === 'minIntervalSeconds');
  const urlIssue = validationIssues.find((issue) => issue.field === 'url');
  const headerIssue = validationIssues.find((issue) => issue.field === 'headers');
  const eventFamilies: WebhookEventFamily[] = webhook.targetKind === 'client' ? ['client'] : ['tunnel', 'p2p'];

  const updateHeader = (headerId: string, field: 'key' | 'value', value: string) => {
    onUpdate('headers', webhook.headers.map((header) => (
      header.id === headerId ? { ...header, [field]: value } : header
    )));
  };

  const confirmTargetKindChange = () => {
    const targetKind = pendingTargetKind;
    if (!targetKind) return;
    const events: WebhookEventKey[] = [];
    onUpdate('targetKind', targetKind);
    onUpdate('targetMode', 'all');
    onUpdate('targetIds', []);
    onUpdate('events', events);
    onUpdate('body', catalog.default_body);
    setSampleEvent(targetKind === 'client' ? 'client.online' : 'tunnel.runtime_changed');
    setPendingTargetKind(null);
    const retainedTemplates = [
      ...getTemplateIssues(webhook.url, events, 'url', catalog.variables),
      ...webhook.headers.flatMap((header) => getTemplateIssues(header.value, events, 'header', catalog.variables)),
    ];
    if (retainedTemplates.length > 0) toast.error(t('webhooks.target.retainedTemplateWarning'));
  };

  return (
    <motion.div
      key={webhook.id}
      initial={{ opacity: 0, y: 4 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.18 }}
      className="flex flex-col gap-5"
    >
      <EditorSection title={t('webhooks.basic.title')}>
        <FieldGroup>
          <Field data-webhook-field="name" data-invalid={Boolean(nameIssue)}>
            <FieldLabel htmlFor={`webhook-name-${webhook.id}`}>{t('webhooks.basic.name')}</FieldLabel>
            <Input
              id={`webhook-name-${webhook.id}`}
              value={webhook.name}
              onChange={(event) => onUpdate('name', event.target.value)}
              placeholder={t('webhooks.basic.namePlaceholder')}
              aria-invalid={Boolean(nameIssue)}
            />
            {nameIssue ? <FieldError>{validationIssueMessage(t, nameIssue)}</FieldError> : null}
          </Field>
          {!isNew ? (
            <Field orientation="horizontal">
              <FieldContent><FieldLabel htmlFor={`webhook-enabled-${webhook.id}`}>{t('webhooks.basic.enabled')}</FieldLabel></FieldContent>
              <Switch
                id={`webhook-enabled-${webhook.id}`}
                checked={webhook.enabled}
                onCheckedChange={(value) => value ? onUpdate('enabled', true) : setDisableOpen(true)}
              />
            </Field>
          ) : null}
        </FieldGroup>
      </EditorSection>

      <ListeningTargetSection
        webhook={webhook}
        clientTargets={clientTargets}
        tunnelTargets={tunnelTargets}
        validationIssues={validationIssues}
        onUpdate={onUpdate}
        onTargetKindChange={setPendingTargetKind}
      />

      <EditorSection title={t('webhooks.events.title')}>
        <div data-webhook-field="events" className="flex flex-col gap-3">
          {eventFamilies.map((family) => {
            const options = catalog.events.filter((option) => option.target_kind === webhook.targetKind && option.family === family);
            return (
              <FieldSet key={family}>
                <FieldLegend variant="label" className={cn(eventFamilies.length === 1 && 'sr-only')}>
                  <span className="inline-flex items-center gap-1">
                    {t(`webhooks.events.group.${family}`)}
                    {family !== 'client' ? <EventFamilyHelp family={family} /> : null}
                  </span>
                </FieldLegend>
                <FieldGroup data-slot="checkbox-group" className="grid gap-2 sm:grid-cols-2">
                  {options.map((option) => {
                    const id = `event-${webhook.id}-${option.key}`;
                    return (
                      <Field key={option.key} orientation="horizontal">
                        <Checkbox
                          id={id}
                          checked={webhook.events.includes(option.key)}
                          onCheckedChange={(checked) => onUpdate('events', checked
                            ? [...new Set([...webhook.events, option.key])]
                            : webhook.events.filter((eventKey) => eventKey !== option.key))}
                        />
                        <FieldContent>
                          <FieldLabel htmlFor={id}>{t(eventLabelKey(option.key))}</FieldLabel>
                        </FieldContent>
                      </Field>
                    );
                  })}
                </FieldGroup>
              </FieldSet>
            );
          })}
          {eventIssue ? <FieldError>{validationIssueMessage(t, eventIssue)}</FieldError> : null}
          <Field data-webhook-field="minIntervalSeconds" data-invalid={Boolean(intervalIssue)}>
            <FieldLabel htmlFor={`webhook-interval-${webhook.id}`}>{t('webhooks.events.minInterval')}</FieldLabel>
            <Input
              id={`webhook-interval-${webhook.id}`}
              type="number"
              min={0}
              max={86400}
              step={1}
              value={webhook.minIntervalSeconds}
              onChange={(event) => onUpdate('minIntervalSeconds', Number(event.target.value))}
              aria-invalid={Boolean(intervalIssue)}
              aria-describedby={`webhook-interval-hint-${webhook.id}`}
              className="max-w-48"
            />
            <FieldDescription id={`webhook-interval-hint-${webhook.id}`}>
              {t('webhooks.events.minIntervalHint')}
            </FieldDescription>
            {intervalIssue ? <FieldError>{validationIssueMessage(t, intervalIssue)}</FieldError> : null}
          </Field>
        </div>
      </EditorSection>

      <EditorSection
        title={t('webhooks.request.title')}
        action={(
          <Button variant="outline" size="sm" onClick={onTest}>
            <Send data-icon="inline-start" />
            {t('webhooks.editor.test')}
          </Button>
        )}
      >
        <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_350px]">
          <FieldGroup>
            <Field>
              <FieldLabel>{t('webhooks.request.method')}</FieldLabel>
              <ToggleGroup
                type="single"
                value={webhook.method}
                onValueChange={(value) => value && onUpdate('method', value as WebhookMethod)}
                variant="outline"
                spacing={0}
              >
                <ToggleGroupItem value="GET" className="px-6">GET</ToggleGroupItem>
                <ToggleGroupItem value="POST" className="px-6">POST</ToggleGroupItem>
              </ToggleGroup>
            </Field>

            <Field data-webhook-field="url" data-invalid={Boolean(urlIssue)}>
              <FieldLabel>{t('webhooks.request.url')}</FieldLabel>
              <TemplatedInput
                value={webhook.url}
                webhook={webhook}
                catalog={catalog}
                events={webhook.events}
                sampleEvent={effectiveSampleEvent}
                surface="url"
                onChange={(value) => onUpdate('url', value)}
                placeholder={t('webhooks.request.urlPlaceholder')}
                aria-invalid={Boolean(urlIssue)}
              />
              {urlIssue ? <FieldError>{validationIssueMessage(t, urlIssue)}</FieldError> : null}
            </Field>

            <Field data-webhook-field="headers" data-invalid={Boolean(headerIssue)}>
              <div className="flex items-center justify-between gap-2">
                <FieldLabel>{t('webhooks.request.headers')}</FieldLabel>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => onUpdate('headers', [...webhook.headers, { id: `header-${Date.now()}`, key: '', value: '' }])}
                >
                  <Plus data-icon="inline-start" />
                  {t('webhooks.request.addHeader')}
                </Button>
              </div>
              <div className="flex flex-col gap-2">
                {webhook.headers.map((header) => (
                  <div key={header.id} className="grid gap-2 sm:grid-cols-[minmax(120px,0.65fr)_minmax(180px,1.35fr)_auto]">
                    <Input
                      value={header.key}
                      onChange={(event) => updateHeader(header.id, 'key', event.target.value)}
                      placeholder={t('webhooks.request.headerName')}
                      className="font-mono text-xs"
                      aria-invalid={Boolean(headerIssue)}
                    />
                    <TemplatedInput
                      value={header.value}
                      webhook={webhook}
                      catalog={catalog}
                      events={webhook.events}
                      sampleEvent={effectiveSampleEvent}
                      surface="header"
                      onChange={(value) => updateHeader(header.id, 'value', value)}
                      placeholder={t('webhooks.request.headerValue')}
                      aria-invalid={Boolean(headerIssue)}
                    />
                    <Button variant="ghost" size="icon" onClick={() => onUpdate('headers', webhook.headers.filter((item) => item.id !== header.id))} aria-label={t('webhooks.request.removeHeader')}>
                      <Trash2 />
                    </Button>
                  </div>
                ))}
                {webhook.headers.length === 0 ? <FieldDescription>{t('webhooks.request.emptyHeaders')}</FieldDescription> : null}
              </div>
              {headerIssue ? <FieldError>{validationIssueMessage(t, headerIssue)}</FieldError> : null}
            </Field>

            {webhook.method === 'POST' ? (
              <Field data-webhook-field="body" data-invalid={Boolean(liveBodyIssue)}>
                <FieldLabel>{t('webhooks.request.body')}</FieldLabel>
                <JsonBodyEditor
                  value={webhook.body}
                  webhook={webhook}
                  catalog={catalog}
                  events={webhook.events}
                  sampleEvent={effectiveSampleEvent}
                  invalid={Boolean(liveBodyIssue)}
                  onChange={(value) => onUpdate('body', value)}
                />
                {liveBodyIssue ? <FieldError>{validationIssueMessage(t, liveBodyIssue)}</FieldError> : null}
              </Field>
            ) : null}
          </FieldGroup>

          <RequestPreview webhook={webhook} catalog={catalog} sampleEvent={effectiveSampleEvent} onSampleEventChange={setSampleEvent} />
        </div>
      </EditorSection>

      <AlertDialog open={pendingTargetKind !== null} onOpenChange={(nextOpen) => !nextOpen && setPendingTargetKind(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('webhooks.target.changeTitle')}</AlertDialogTitle>
            <AlertDialogDescription>{t('webhooks.target.changeDescription')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={confirmTargetKindChange}>{t('webhooks.target.changeConfirm')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={disableOpen} onOpenChange={setDisableOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('webhooks.disable.title')}</AlertDialogTitle>
            <AlertDialogDescription>{t('webhooks.disable.description')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => onUpdate('enabled', false)}>{t('webhooks.disable.confirm')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </motion.div>
  );
}

function EventFamilyHelp({ family }: { family: Extract<WebhookEventFamily, 'tunnel' | 'p2p'> }) {
  const { t } = useTranslation();
  const items = family === 'tunnel'
    ? ['intent', 'changed', 'error'] as const
    : ['checking', 'connected', 'failure', 'closed'] as const;
  const label = t(`webhooks.events.help.${family}.label`);

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          className="-my-1 text-muted-foreground"
          aria-label={label}
          title={label}
        >
          <CircleHelp />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" collisionPadding={8} className="w-80 max-w-[calc(100vw-1rem)]">
        <PopoverHeader>
          <PopoverTitle>{t(`webhooks.events.help.${family}.title`)}</PopoverTitle>
          <PopoverDescription className="text-xs leading-relaxed">
            {t(`webhooks.events.help.${family}.description`)}
          </PopoverDescription>
        </PopoverHeader>
        <dl className="flex flex-col gap-2 text-xs leading-relaxed">
          {items.map((item) => (
            <div key={item}>
              <dt className="font-medium">{t(`webhooks.events.help.${family}.item.${item}.title`)}</dt>
              <dd className="text-muted-foreground">{t(`webhooks.events.help.${family}.item.${item}.description`)}</dd>
            </div>
          ))}
        </dl>
      </PopoverContent>
    </Popover>
  );
}

function ListeningTargetSection({
  webhook,
  clientTargets,
  tunnelTargets,
  validationIssues,
  onUpdate,
  onTargetKindChange,
}: {
  webhook: WebhookPrototype;
  clientTargets: WebhookTargetOption[];
  tunnelTargets: WebhookTargetOption[];
  validationIssues: WebhookValidationIssue[];
  onUpdate: <Key extends keyof WebhookPrototype>(key: Key, value: WebhookPrototype[Key]) => void;
  onTargetKindChange: (targetKind: WebhookTargetKind) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const targetIssue = validationIssues.find((issue) => issue.field === 'targets');
  const availableTargets = webhook.targetKind === 'client' ? clientTargets : tunnelTargets;
  const targets = [
    ...availableTargets,
    ...webhook.targetIds
      .filter((id) => !availableTargets.some((target) => target.id === id))
      .map((id) => ({ id, name: id, detail: t('webhooks.target.unavailable'), unavailable: true })),
  ];
  const filteredTargets = targets.filter((target) => (
    (!target.unavailable || webhook.targetIds.includes(target.id))
    && `${target.name} ${target.detail} ${target.id}`.toLowerCase().includes(query.trim().toLowerCase())
  ));
  const toggleTarget = (targetId: string, checked: boolean) => {
    onUpdate('targetIds', checked
      ? [...new Set([...webhook.targetIds, targetId])]
      : webhook.targetIds.filter((id) => id !== targetId));
  };

  return (
    <EditorSection title={t('webhooks.target.title')}>
      <FieldGroup>
        <Field>
          <FieldLabel>{t('webhooks.target.kind')}</FieldLabel>
          <ToggleGroup
            type="single"
            value={webhook.targetKind}
            onValueChange={(value) => value && value !== webhook.targetKind && onTargetKindChange(value as WebhookTargetKind)}
            variant="outline"
            spacing={2}
            className="grid w-full grid-cols-2"
          >
            <ToggleGroupItem value="client" className="h-10 justify-start px-3 text-left whitespace-normal">
              <Server />
              <span className="text-sm font-medium">{t('webhooks.target.client')}</span>
            </ToggleGroupItem>
            <ToggleGroupItem value="tunnel" className="h-10 justify-start px-3 text-left whitespace-normal">
              <Waypoints />
              <span className="text-sm font-medium">{t('webhooks.target.tunnel')}</span>
            </ToggleGroupItem>
          </ToggleGroup>
        </Field>

        <Field>
          <FieldLabel>{t('webhooks.target.range')}</FieldLabel>
          <ToggleGroup
            type="single"
            value={webhook.targetMode}
            onValueChange={(value) => value && onUpdate('targetMode', value as WebhookPrototype['targetMode'])}
            variant="outline"
            spacing={0}
          >
            <ToggleGroupItem value="all" className="px-5">{t('webhooks.target.all')}</ToggleGroupItem>
            <ToggleGroupItem value="selected" className="px-5">{t('webhooks.target.selected')}</ToggleGroupItem>
          </ToggleGroup>
        </Field>

        {webhook.targetMode === 'selected' ? (
          <Field data-webhook-field="targets" data-invalid={Boolean(targetIssue)}>
            <FieldLabel>{t(`webhooks.target.${webhook.targetKind === 'client' ? 'selectedClients' : 'selectedTunnels'}`)}</FieldLabel>
            <InputGroup>
              <InputGroupAddon><Search /></InputGroupAddon>
              <InputGroupInput value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('webhooks.target.searchPlaceholder')} />
            </InputGroup>
            {filteredTargets.length > 0 ? (
              <FieldGroup data-slot="checkbox-group" className="grid gap-2 sm:grid-cols-2">
                {filteredTargets.map((target) => {
                  const id = `target-${webhook.id}-${target.id}`;
                  return (
                    <Field key={target.id} orientation="horizontal">
                      <Checkbox id={id} checked={webhook.targetIds.includes(target.id)} onCheckedChange={(checked) => toggleTarget(target.id, checked === true)} />
                      <FieldContent>
                        <div className="flex flex-wrap items-center gap-1.5">
                          <FieldLabel htmlFor={id}>{target.name}</FieldLabel>
                          {target.unavailable ? <Badge variant="outline">{t('webhooks.target.unavailable')}</Badge> : null}
                        </div>
                        <FieldDescription>{target.detail}</FieldDescription>
                      </FieldContent>
                    </Field>
                  );
                })}
              </FieldGroup>
            ) : (
              <Empty className="min-h-28 border border-dashed">
                <EmptyHeader><EmptyTitle>{t('webhooks.target.noResults')}</EmptyTitle></EmptyHeader>
              </Empty>
            )}
            {targetIssue ? <FieldError>{validationIssueMessage(t, targetIssue)}</FieldError> : null}
          </Field>
        ) : null}
      </FieldGroup>
    </EditorSection>
  );
}

function EditorSection({ title, action, children }: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3 border-b pb-5 last:border-b-0 last:pb-0">
      <header className="flex items-start justify-between gap-3">
        <h2 className="text-sm font-medium">{title}</h2>
        {action}
      </header>
      {children}
    </section>
  );
}

function TemplatedInput({ value, onChange, webhook, catalog, events, sampleEvent, surface, ...props }: Omit<React.ComponentProps<typeof InputGroupInput>, 'value' | 'onChange'> & {
  value: string;
  onChange: (value: string) => void;
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  events: WebhookEventKey[];
  sampleEvent: WebhookEventKey;
  surface: Exclude<WebhookTemplateSurface, 'body'>;
}) {
  return (
    <InputGroup>
      <InputGroupInput value={value} onChange={(event) => onChange(event.target.value)} className="font-mono text-xs" {...props} />
      <VariablePicker
        events={events}
        sampleEvent={sampleEvent}
        webhook={webhook}
        catalog={catalog}
        surface={surface}
      />
    </InputGroup>
  );
}

function JsonBodyEditor({ value, onChange, webhook, catalog, invalid, events, sampleEvent }: {
  value: string;
  onChange: (value: string) => void;
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  invalid: boolean;
  events: WebhookEventKey[];
  sampleEvent: WebhookEventKey;
}) {
  const { t } = useTranslation();
  const editorRef = useRef<WebhookJsonEditorHandle>(null);
  const formatJson = () => {
    if (!editorRef.current?.format()) toast.error(t('webhooks.validation.invalidJson'));
  };

  return (
    <div className="overflow-hidden rounded-lg border bg-muted/10">
      <div className={cn('flex flex-wrap items-center gap-2 border-b bg-background px-2.5 py-2', invalid ? 'justify-between' : 'justify-end')}>
        {invalid ? (
          <div className="flex items-center gap-1.5 text-xs text-destructive">
            <CircleOff className="size-3.5" />
            {t('webhooks.preview.invalidTemplate')}
          </div>
        ) : null}
        <div className="flex items-center gap-1.5">
          <VariablePicker
            standalone
            events={events}
            sampleEvent={sampleEvent}
            webhook={webhook}
            catalog={catalog}
            surface="body"
          />
          <Button type="button" variant="outline" size="sm" onClick={formatJson}>
            <AlignLeft data-icon="inline-start" />
            {t('webhooks.editor.formatJson')}
          </Button>
        </div>
      </div>
      <WebhookJsonEditor
        ref={editorRef}
        value={value}
        onChange={onChange}
        invalid={invalid}
        events={events}
        sampleEvent={sampleEvent}
        webhook={webhook}
        catalog={catalog}
        label={t('webhooks.request.body')}
        className="rounded-none border-0 bg-transparent focus-within:ring-0"
      />
    </div>
  );
}

function VariablePicker({ webhook, catalog, events, sampleEvent, surface, standalone = false }: {
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  events: WebhookEventKey[];
  sampleEvent: WebhookEventKey;
  surface: WebhookTemplateSurface;
  standalone?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const [open, setOpen] = useState(false);
  const [language, setLanguage] = useState(() => resolvedPickerLanguage(i18n.resolvedLanguage, catalog.locales));
  const groups = ['delivery', 'event', 'client', 'tunnel', 'webhook'] as const;
  const variables = useMemo(
    () => getPickerVariables(catalog, events, surface, language),
    [catalog, events, surface, language],
  );

  // The popover stays open after copying so several variables can be taken in one visit;
  // it closes through the usual outside click / Escape.
  const selectVariable = async (variable: PickerVariable) => {
    try {
      await copyText(`{{${variable.variable.key}}}`);
      toast.success(t('webhooks.variables.copied', { key: `{{${variable.variable.key}}}` }));
    } catch {
      toast.error(t('webhooks.variables.copyFailed'));
    }
  };

  return (
    <Popover modal open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        {standalone ? (
          <Button type="button" variant="outline" size="sm">
            <Braces data-icon="inline-start" />
            {t('webhooks.variables.title')}
          </Button>
        ) : (
          <InputGroupButton type="button" size="icon-xs" aria-label={t('webhooks.variables.title')}><Braces /></InputGroupButton>
        )}
      </PopoverTrigger>
      <PopoverContent align="end" collisionPadding={8} className="w-[28rem] max-w-[calc(100vw-1rem)] p-0">
        <PopoverHeader className="flex-row items-center justify-between border-b p-3">
          <PopoverTitle>{t('webhooks.variables.title')}</PopoverTitle>
          {catalog.locales.length > 1 ? (
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              value={language}
              onValueChange={(value) => { if (value) setLanguage(value); }}
            >
              {catalog.locales.map((locale) => (
                <ToggleGroupItem key={locale} value={locale}>{t(`webhooks.variables.language.${locale}`)}</ToggleGroupItem>
              ))}
            </ToggleGroup>
          ) : null}
        </PopoverHeader>
        <div className="p-2">
          <div data-slot="webhook-variable-list" className="max-h-80 overflow-y-auto overscroll-contain pr-1">
            {groups.map((group) => {
              const groupVariables = variables.filter((entry) => entry.variable.group === group);
              if (groupVariables.length === 0) return null;
              return (
                <section key={group} className="mb-2 last:mb-0">
                  <h3 className="px-2 py-1 text-xs font-medium text-muted-foreground">{t(`webhooks.variables.group.${group}`)}</h3>
                  {groupVariables.map((entry) => {
                    const sample = webhookVariableSample(catalog, entry.variable, sampleEvent, webhook);
                    return (
                      <button
                        key={entry.baseKey}
                        type="button"
                        onClick={() => selectVariable(entry)}
                        className="grid w-full grid-cols-[minmax(0,1fr)_minmax(0,0.8fr)] items-center gap-3 rounded-md px-2 py-2 text-left hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                      >
                        <span className="min-w-0">
                          <span className="block truncate text-xs font-medium">
                            {t(variableLabelKey(entry.baseKey))}
                            {entry.variable.optional ? ` · ${t('webhooks.variables.optional')}` : ''}
                          </span>
                          <code className="block truncate text-[10px] text-muted-foreground">{`{{${entry.variable.key}}}`}</code>
                        </span>
                        <code className="truncate text-right text-[11px]" title={sample}>{sample}</code>
                      </button>
                    );
                  })}
                </section>
              );
            })}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function RequestPreview({ webhook, catalog, sampleEvent, onSampleEventChange }: {
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  sampleEvent: WebhookEventKey;
  onSampleEventChange: (event: WebhookEventKey) => void;
}) {
  const { t } = useTranslation();
  const previewEvents = webhook.events;
  if (previewEvents.length === 0) {
    return (
      <aside className="rounded-lg border bg-muted/10 p-3 xl:sticky xl:top-3">
        <div className="flex items-start gap-2">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground"><FileJson2 className="size-4" /></span>
          <div>
            <h3 className="text-sm font-medium">{t('webhooks.preview.title')}</h3>
          </div>
        </div>
        <Empty className="min-h-40 p-4">
          <EmptyHeader><EmptyTitle>{t('webhooks.preview.empty')}</EmptyTitle></EmptyHeader>
        </Empty>
      </aside>
    );
  }
  const effectiveEvent = previewEvents.includes(sampleEvent) ? sampleEvent : previewEvents[0];
  const request = renderWebhookRequest(webhook, effectiveEvent, catalog);

  return (
    <aside className="rounded-lg border bg-muted/10 p-3 xl:sticky xl:top-3">
      <div className="mb-3 flex items-start gap-2">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground"><FileJson2 className="size-4" /></span>
        <div>
          <h3 className="text-sm font-medium">{t('webhooks.preview.title')}</h3>
        </div>
      </div>
      <Select value={effectiveEvent} onValueChange={(value) => onSampleEventChange(value as WebhookEventKey)} disabled={webhook.events.length === 0}>
        <SelectTrigger className="mb-3 w-full"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectGroup>
            {previewEvents.map((event) => <SelectItem key={event} value={event}>{t(eventLabelKey(event))}</SelectItem>)}
          </SelectGroup>
        </SelectContent>
      </Select>
      <div className="flex flex-col gap-3">
        <PreviewBlock title={t('webhooks.preview.url')}>
          <div className="flex items-start gap-2"><Badge variant="outline">{webhook.method}</Badge><code className="break-all text-[10px] leading-relaxed">{request.url}</code></div>
        </PreviewBlock>
        <PreviewBlock title={t('webhooks.preview.headers')}>
          {request.headers.length > 0 ? request.headers.map((header, index) => (
            <div key={`${header.key}-${index}`} className="grid grid-cols-[auto_1fr] gap-2 font-mono text-[10px]"><span className="text-muted-foreground">{header.key}</span><span className="break-all">{header.value}</span></div>
          )) : <span className="text-xs text-muted-foreground">{t('webhooks.preview.noHeaders')}</span>}
        </PreviewBlock>
        <PreviewBlock title={t('webhooks.preview.body')}>
          {webhook.method === 'GET' ? <span className="text-xs text-muted-foreground">{t('webhooks.preview.noBody')}</span> : (
            <>
              {request.bodyError ? (
                <div className="mb-1.5 flex items-center gap-1 text-[11px] text-destructive">
                  <CircleOff className="size-3" />
                  {t('webhooks.preview.invalidTemplate')}
                </div>
              ) : null}
              <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all font-mono text-[10px] leading-relaxed">{request.body}</pre>
            </>
          )}
        </PreviewBlock>
      </div>
    </aside>
  );
}

function PreviewBlock({ title, children }: { title: string; children: React.ReactNode }) {
  return <section><h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</h4><div className="rounded-lg border bg-background p-2">{children}</div></section>;
}

function TestWebhookDialog({ open, webhook, catalog, onOpenChange }: {
  open: boolean;
  webhook: WebhookPrototype;
  catalog: WebhookCatalog;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation();
  const [event, setEvent] = useState<WebhookEventKey>(webhook.events[0]);
  const [deliveryId, setDeliveryId] = useState<string | null>(null);
  const testWebhook = useTestWebhook();
  const deliveryQuery = useWebhookDelivery(deliveryId);
  const effectiveEvent = webhook.events.includes(event) ? event : webhook.events[0];
  const request = renderWebhookRequest(webhook, effectiveEvent, catalog);
  const delivery = deliveryQuery.data ?? testWebhook.data;
  const complete = delivery?.status === 'success' || delivery?.status === 'failed' || delivery?.status === 'canceled';

  const sendTest = async () => {
    try {
      const queued = await testWebhook.mutateAsync({ config: webhook, event: effectiveEvent });
      setDeliveryId(queued.id);
      toast.success(t('webhooks.toast.testQueued'));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('webhooks.toast.operationFailed'));
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t('webhooks.test.title')}</DialogTitle>
          <DialogDescription>{t('webhooks.test.description')}</DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel>{t('webhooks.preview.sampleEvent')}</FieldLabel>
            <Select value={effectiveEvent} onValueChange={(value) => {
              setEvent(value as WebhookEventKey);
              setDeliveryId(null);
              testWebhook.reset();
            }} disabled={testWebhook.isPending || Boolean(delivery && !complete)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>{webhook.events.map((item) => <SelectItem key={item} value={item}>{t(eventLabelKey(item))}</SelectItem>)}</SelectGroup></SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel>{t('webhooks.test.request')}</FieldLabel>
            <div className="rounded-lg border bg-muted/20 p-3">
              <div className="flex items-start gap-2"><Badge variant="outline">{webhook.method}</Badge><code className="break-all text-xs">{request.url}</code></div>
            </div>
          </Field>
          {delivery ? (
            <div className="grid grid-cols-3 gap-2">
              <Metric label={t('webhooks.deliveries.result')} value={t(`webhooks.status.${delivery.status}`)} />
              <Metric label={t('webhooks.deliveries.response')} value={String(delivery.statusCode ?? '—')} />
              <Metric label={t('webhooks.deliveries.duration')} value={delivery.durationMs === null ? '—' : `${delivery.durationMs} ms`} />
            </div>
          ) : null}
          {delivery?.error ? <FieldError>{delivery.error}</FieldError> : null}
        </FieldGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{complete ? t('common.close') : t('common.cancel')}</Button>
          {!delivery ? <Button onClick={() => void sendTest()} disabled={testWebhook.isPending}><Send data-icon="inline-start" />{t('webhooks.test.send')}</Button> : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function WebhookDeliveryLog({ webhookId, invocations, status, onStatusChange, hasMore, loadingMore, onLoadMore, onReplay }: {
  webhookId: string;
  invocations: WebhookInvocation[];
  status: 'all' | WebhookInvocationStatus;
  onStatusChange: (status: 'all' | WebhookInvocationStatus) => void;
  hasMore: boolean;
  loadingMore: boolean;
  onLoadMore: () => void;
  onReplay: (invocation: WebhookInvocation) => void;
}) {
  const { t, i18n } = useTranslation();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const filtered = invocations.filter((item) => item.webhookId === webhookId);

  return (
    <>
      <div className="mb-3 flex justify-end">
        <Select value={status} onValueChange={(value) => onStatusChange(value as typeof status)}>
          <SelectTrigger size="sm" className="w-full sm:w-40"><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>
            {(['all', 'queued', 'retrying', 'success', 'failed', 'canceled'] as const).map((value) => <SelectItem key={value} value={value}>{t(`webhooks.deliveries.filter.${value}`)}</SelectItem>)}
          </SelectGroup></SelectContent>
        </Select>
      </div>
      {filtered.length > 0 ? (
        <>
          <div className="flex flex-col gap-2 sm:hidden">
            {filtered.map((invocation) => (
              <button
                key={invocation.id}
                type="button"
                onClick={() => setSelectedId(invocation.id)}
                className="rounded-lg border bg-background p-3 text-left transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                aria-label={`${t(eventLabelKey(invocation.event))} ${t('webhooks.deliveries.details')}`}
              >
                <div className="flex items-start justify-between gap-3">
                  <span className="font-medium">{t(eventLabelKey(invocation.event))}</span>
                  <Badge variant={statusVariant(invocation.status)}>{t(`webhooks.status.${invocation.status}`)}</Badge>
                </div>
                <div className="mt-1.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                  <span>{t(`webhooks.deliveries.originValue.${invocation.origin}`)}</span>
                  <span aria-hidden>·</span>
                  <time>{formatShortTime(invocation.occurredAt, i18n.resolvedLanguage ?? 'zh-CN')}</time>
                </div>
                <div className="mt-2 flex items-center gap-4 font-mono text-xs">
                  <span>{t('webhooks.deliveries.response')} {invocation.statusCode ?? '—'}</span>
                  <span>{invocation.durationMs === null ? '—' : `${invocation.durationMs} ms`}</span>
                </div>
              </button>
            ))}
          </div>
          <div className="hidden overflow-x-auto rounded-lg border sm:block">
            <Table>
              <TableHeader><TableRow><TableHead>{t('webhooks.deliveries.event')}</TableHead><TableHead>{t('webhooks.deliveries.origin')}</TableHead><TableHead>{t('webhooks.deliveries.result')}</TableHead><TableHead>{t('webhooks.deliveries.response')}</TableHead><TableHead>{t('webhooks.deliveries.duration')}</TableHead><TableHead>{t('webhooks.deliveries.time')}</TableHead><TableHead /></TableRow></TableHeader>
              <TableBody>
                {filtered.map((invocation) => (
                  <TableRow key={invocation.id}>
                    <TableCell><div className="font-medium">{t(eventLabelKey(invocation.event))}</div><code className="text-[10px] text-muted-foreground">{invocation.event}</code></TableCell>
                    <TableCell><Badge variant="outline">{t(`webhooks.deliveries.originValue.${invocation.origin}`)}</Badge></TableCell>
                    <TableCell><Badge variant={statusVariant(invocation.status)}>{t(`webhooks.status.${invocation.status}`)}</Badge></TableCell>
                    <TableCell className="font-mono text-xs">{invocation.statusCode ?? '—'}</TableCell>
                    <TableCell className="font-mono text-xs">{invocation.durationMs === null ? '—' : `${invocation.durationMs} ms`}</TableCell>
                    <TableCell className="text-xs">{formatShortTime(invocation.occurredAt, i18n.resolvedLanguage ?? 'zh-CN')}</TableCell>
                    <TableCell className="text-right"><Button variant="outline" size="sm" onClick={() => setSelectedId(invocation.id)}>{t('webhooks.deliveries.details')}</Button></TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {hasMore ? (
            <div className="mt-3 flex justify-center">
              <Button variant="outline" size="sm" disabled={loadingMore} onClick={onLoadMore}>{loadingMore ? t('webhooks.deliveries.loadingMore') : t('webhooks.deliveries.loadMore')}</Button>
            </div>
          ) : null}
        </>
      ) : (
        <Empty className="min-h-56 border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon"><Clock3 /></EmptyMedia>
            <EmptyTitle>{t('webhooks.deliveries.empty')}</EmptyTitle>
            <EmptyDescription>{status === 'all' ? t('webhooks.deliveries.emptyDescription') : t('webhooks.deliveries.emptyFiltered')}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      )}
      <DeliveryDialog invocationId={selectedId} initial={invocations.find((item) => item.id === selectedId) ?? null} onOpenChange={(nextOpen) => !nextOpen && setSelectedId(null)} onReplay={onReplay} />
    </>
  );
}

function DeliveryDialog({ invocationId, initial, onOpenChange, onReplay }: {
  invocationId: string | null;
  initial: WebhookInvocation | null;
  onOpenChange: (open: boolean) => void;
  onReplay: (invocation: WebhookInvocation) => void;
}) {
  const { t } = useTranslation();
  const [replayOpen, setReplayOpen] = useState(false);
  // The live query keeps an open dialog in sync while the delivery is still
  // queued or retrying; `initial` covers the moment before the first fetch.
  const { data: live } = useWebhookDelivery(invocationId);
  const invocation = live ?? initial;
  const requestHeaders = invocation ? JSON.stringify(invocation.requestHeaders, null, 2) : '';
  const responseHeaders = invocation ? JSON.stringify(invocation.responseHeaders, null, 2) : '';
  return (
    <>
      <Dialog open={Boolean(invocationId)} onOpenChange={onOpenChange}>
        <DialogContent className="max-h-[85vh] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-2xl">
          <DialogHeader><DialogTitle>{t('webhooks.detail.title')}</DialogTitle><DialogDescription className="sr-only">{t('webhooks.detail.description')}</DialogDescription></DialogHeader>
          {invocation ? (
            <div className="flex min-h-0 flex-col gap-4 overflow-y-auto pr-1">
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                <Metric label={t('webhooks.deliveries.result')} value={t(`webhooks.status.${invocation.status}`)} />
                <Metric label={t('webhooks.deliveries.response')} value={String(invocation.statusCode ?? '—')} />
                <Metric label={t('webhooks.deliveries.duration')} value={invocation.durationMs === null ? '—' : `${invocation.durationMs} ms`} />
                <Metric label={t('webhooks.deliveries.attempt')} value={String(invocation.attempts.length)} />
              </div>
              {invocation.error ? <FieldError>{invocation.error}</FieldError> : null}
              <div>
                <div className="mb-2 text-xs font-medium">{t('webhooks.detail.attempts')}</div>
                <div className="flex flex-col gap-2">
                  {invocation.attempts.map((attempt) => (
                    <div key={attempt.number} className="grid grid-cols-[auto_1fr_auto] items-center gap-3 rounded-lg border px-3 py-2 text-xs">
                      <Badge variant={attempt.status === 'failed' ? 'destructive' : attempt.status === 'success' ? 'secondary' : 'outline'}>#{attempt.number}</Badge>
                      <span>{t(`webhooks.detail.attemptStatus.${attempt.status}`)}{attempt.error ? ` · ${attempt.error}` : ''}</span>
                      <code>{attempt.statusCode ?? '—'} · {attempt.durationMs === null ? '—' : `${attempt.durationMs} ms`}</code>
                    </div>
                  ))}
                </div>
              </div>
              <CodeBlock label={t('webhooks.detail.requestUrl')} value={invocation.requestUrl} />
              <CodeBlock label={t('webhooks.detail.requestHeaders')} value={requestHeaders} />
              <CodeBlock label={t('webhooks.detail.requestBody')} value={prettyJson(invocation.requestBody) || '—'} />
              <CodeBlock label={t('webhooks.detail.responseHeaders')} value={responseHeaders} />
              <CodeBlock label={t('webhooks.detail.responseBody')} value={prettyJson(invocation.responseBody) || '—'} />
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => { if (invocation) void navigator.clipboard?.writeText(invocation.id); toast.success(t('webhooks.toast.copied')); }}><Copy data-icon="inline-start" />{t('webhooks.detail.copyId')}</Button>
            {invocation?.origin !== 'test' ? <Button onClick={() => setReplayOpen(true)}><RotateCw data-icon="inline-start" />{t('webhooks.detail.replay')}</Button> : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog open={replayOpen} onOpenChange={setReplayOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('webhooks.replay.title')}</AlertDialogTitle>
            <AlertDialogDescription>{t('webhooks.replay.description')}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => { if (invocation) onReplay(invocation); setReplayOpen(false); }}>{t('webhooks.replay.confirm')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div className="rounded-lg border bg-muted/15 p-2"><div className="text-[10px] uppercase tracking-wider text-muted-foreground">{label}</div><div className="mt-1 text-sm font-medium">{value}</div></div>;
}

function CodeBlock({ label, value }: { label: string; value: string }) {
  const truncated = value.length > 6000 ? `${value.slice(0, 6000)}\n…` : value;
  return <div><div className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</div><pre className="max-h-52 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted/50 p-2.5 font-mono text-[11px] leading-relaxed">{truncated}</pre></div>;
}

function prettyJson(value: string | null) {
  if (!value) return '';
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}
