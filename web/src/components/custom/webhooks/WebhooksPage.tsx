import { lazy, Suspense, useState } from "react";
import { motion } from "motion/react";
import { Plus, Webhook as WebhookIcon } from "lucide-react";
import toast from "react-hot-toast";
import { useTranslation } from "react-i18next";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useToggleWebhook, useWebhookCatalog, useWebhooks } from "@/hooks/use-webhooks";
import { formatShortTime } from "@/lib/format";
import type { ActivityWebhookConfig } from "@/types/webhook";

const LazyActivityWebhookManager = lazy(() =>
  import("@/components/custom/activity/ActivityWebhookManager").then((m) => ({
    default: m.ActivityWebhookManager,
  })),
);

const fadeUp = {
  hidden: { opacity: 0, y: 12 },
  show: { opacity: 1, y: 0, transition: { duration: 0.35, ease: "easeOut" as const } },
};

export function WebhooksPage() {
  const { t, i18n } = useTranslation();
  const { data: catalog } = useWebhookCatalog();
  const { data: webhooks = [], isLoading } = useWebhooks();
  const toggleWebhook = useToggleWebhook();
  const [editing, setEditing] = useState<ActivityWebhookConfig | "new" | null>(
    null,
  );

  const toggleEnabled = async (item: ActivityWebhookConfig, enabled: boolean) => {
    try {
      await toggleWebhook.mutateAsync({ webhookId: item.id, enabled });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("errors.generic"));
    }
  };

  return (
    <motion.div
      variants={{ hidden: {}, show: { transition: { staggerChildren: 0.08 } } }}
      initial="hidden"
      animate="show"
      className="z-10 mx-auto flex w-full max-w-6xl flex-col gap-5 p-4 sm:gap-6 sm:p-6 lg:p-8"
    >
      <motion.div variants={fadeUp} className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h3 className="text-xl font-semibold tracking-tight">
            {t("webhooks.pageTitle")}
          </h3>
          <p className="mt-1 text-sm text-muted-foreground">
            {t("webhooks.pageDescription")}
          </p>
        </div>
        <Button onClick={() => setEditing("new")} disabled={!catalog}>
          <Plus data-icon="inline-start" />
          {t("webhooks.manager.newWebhook")}
        </Button>
      </motion.div>

      <motion.div variants={fadeUp}>
        <section className="rounded-xl border border-border/40 bg-card/50 shadow-sm backdrop-blur-sm">
          <header className="flex items-center justify-between gap-3 rounded-t-xl border-b border-border/40 bg-muted/20 px-3 py-2.5 sm:px-4">
            <div className="text-sm font-medium">
              {t("webhooks.manager.listTitle")}
              <span className="ml-2 text-xs font-normal text-muted-foreground">
                {t("webhooks.manager.configured", { count: webhooks.length })}
              </span>
            </div>
          </header>
          {isLoading ? (
            <div className="space-y-3 p-4">
              <Skeleton className="h-14 w-full" />
              <Skeleton className="h-14 w-full" />
            </div>
          ) : webhooks.length === 0 ? (
            <Empty className="min-h-64">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <WebhookIcon />
                </EmptyMedia>
                <EmptyTitle>{t("webhooks.manager.emptySelection")}</EmptyTitle>
                <EmptyDescription>
                  {t("webhooks.manager.emptyDescription")}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="divide-y divide-border/40">
              {webhooks.map((item) => {
                const calledAt = formatShortTime(
                  item.lastCalledAt,
                  i18n.resolvedLanguage ?? "zh-CN",
                );
                return (
                  <div
                    key={item.id}
                    className="grid gap-3 px-3 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-4 sm:px-4"
                  >
                    <button
                      type="button"
                      className="min-w-0 rounded-md text-left transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                      onClick={() => setEditing(item)}
                    >
                      <span className="block truncate font-medium">
                        {item.name || t("webhooks.manager.webhookFallback")}
                      </span>
                      <span className="mt-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                        <Badge variant="outline" className="h-5 px-1.5 text-[11px]">
                          {t(`webhooks.health.${item.lastStatus}`)}
                        </Badge>
                        {calledAt ? (
                          <span className="truncate">{calledAt}</span>
                        ) : null}
                      </span>
                    </button>
                    <div className="flex items-center gap-3 sm:justify-end">
                      <Switch
                        checked={item.enabled}
                        disabled={toggleWebhook.isPending}
                        onCheckedChange={(enabled) => void toggleEnabled(item, enabled)}
                        aria-label={t(
                          `webhooks.status.${item.enabled ? "enabled" : "disabled"}`,
                        )}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      </motion.div>

      {editing !== null && (
        <Suspense fallback={null}>
          <LazyActivityWebhookManager
            key={editing === "new" ? "new" : editing.id}
            open
            onOpenChange={(nextOpen: boolean) => {
              if (!nextOpen) setEditing(null);
            }}
            editWebhook={editing}
          />
        </Suspense>
      )}
    </motion.div>
  );
}
