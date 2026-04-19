patch -p1 << 'PATCH'
--- a/web/src/lib/format.ts
+++ b/web/src/lib/format.ts
@@ -38,6 +38,24 @@
   return `${formatBytes(bytesPerSec)}/s`;
 }
 
+/** 
+ * 将带宽限制 (bytes/s) 转换为 MB/s 的输入框字符串 
+ * 保留最多 6 位小数以支持小值（如 1 byte/s）准确回显
+ */
+export function bpsToMbpsInput(bytes?: number | null): string {
+  if (!bytes || bytes <= 0) return '';
+  const mbps = bytes / (1024 * 1024);
+  return String(Number(mbps.toFixed(6)));
+}
+
+/** 将输入框的 MB/s 字符串解析为 bytes/s (取整)，留空返回 0 */
+export function parseMbpsInputToBps(value: string): number | null {
+  const trimmed = value.trim();
+  if (trimmed === '') return 0;
+  const parsed = Number.parseFloat(trimmed);
+  if (Number.isNaN(parsed) || parsed < 0) return null;
+  return Math.round(parsed * 1024 * 1024);
+}
+
 /** 将 Unix 时间戳转换为距今时长: 1609459200 → "5 年 73 天" */
 export function formatInstallAge(unixTimestamp: number): string {
   if (!unixTimestamp || unixTimestamp <= 0) return '-';
PATCH
