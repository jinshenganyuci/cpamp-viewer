import type { Language } from "@/types/common";
import type { UsageStatusOperation } from "@/viewer/api/usageStatus";

interface UsageStatusCopy {
  title: string;
  description: string;
  refresh: string;
  refreshing: string;
  loading: string;
  unavailable: string;
  stale: string;
  checked: string;
  unknown: string;
  online: string;
  archived: string;
  deleted: string;
  recordsNote: string;
  range: string;
  earliest: string;
  latest: string;
  emptyRange: string;
  storage: string;
  total: string;
  database: string;
  wal: string;
  shm: string;
  reclaimable: string;
  storageNote: string;
  readiness: string;
  migration: string;
  aggregate: string;
  ready: string;
  pending: string;
  activity: string;
  idle: string;
  pendingTask: string;
  state: string;
  instances: string;
  partial: string;
  instanceActivity: string;
  operations: Record<UsageStatusOperation, string>;
  states: Record<string, string>;
}

const copies: Record<Language, UsageStatusCopy> = {
  "zh-CN": {
    title: "用量状态",
    description: "查看用量记录的保留范围、存储占用与统计准备状态。",
    refresh: "刷新",
    refreshing: "刷新中…",
    loading: "正在读取用量状态…",
    unavailable: "当前服务不支持用量状态，或暂时无法读取。",
    stale: "当前状态暂未更新，以下为上次检查结果。",
    checked: "上次检查",
    unknown: "未知",
    online: "在线明细",
    archived: "已归档的在线明细",
    deleted: "已清理明细",
    recordsNote:
      "已归档的在线明细已包含在在线明细总数中，不含已清理的记录。这三项数量不能直接相加。",
    range: "在线明细时间范围",
    earliest: "最早记录",
    latest: "最新记录",
    emptyRange: "当前没有在线明细。",
    storage: "存储占用",
    total: "总占用",
    database: "数据库",
    wal: "写入日志",
    shm: "共享内存文件",
    reclaimable: "库内可回收空间",
    storageNote:
      "清理明细不会自动缩小数据库文件。库内可回收空间仍包含在数据库占用中。",
    readiness: "统计准备状态",
    migration: "用量数据准备",
    aggregate: "小时聚合",
    ready: "已就绪",
    pending: "尚未就绪",
    activity: "维护活动",
    idle: "当前没有维护任务",
    pendingTask: "有尚未完成的维护任务",
    state: "任务状态",
    instances: "各实例状态",
    partial: "部分实例暂不可用，汇总数据不完整。",
    instanceActivity: "维护任务请查看各实例状态",
    operations: {
      archive: "正在归档",
      verify: "正在校验",
      delete: "正在清理明细",
      compact: "正在回收空间",
      maintenance: "正在维护",
    },
    states: {
      previewed: "已预览",
      archiving: "归档中",
      archived: "已归档",
      verifying: "校验中",
      verified: "已校验",
      deleting: "清理中",
      completed: "已完成",
      failed: "失败",
      cancelled: "已取消",
      compacting: "空间回收中",
      running: "进行中",
      pending: "等待中",
    },
  },
  "zh-TW": {
    title: "用量狀態",
    description: "查看用量記錄的保留範圍、儲存空間與統計準備狀態。",
    refresh: "重新整理",
    refreshing: "更新中…",
    loading: "正在讀取用量狀態…",
    unavailable: "目前服務不支援用量狀態，或暫時無法讀取。",
    stale: "目前狀態尚未更新，以下為上次檢查結果。",
    checked: "上次檢查",
    unknown: "未知",
    online: "線上明細",
    archived: "已封存的線上明細",
    deleted: "已清理明細",
    recordsNote:
      "已封存的線上明細已包含在線上明細總數中，不含已清理的記錄。這三項數量不能直接相加。",
    range: "線上明細時間範圍",
    earliest: "最早記錄",
    latest: "最新記錄",
    emptyRange: "目前沒有線上明細。",
    storage: "儲存空間",
    total: "總占用",
    database: "資料庫",
    wal: "寫入日誌",
    shm: "共享記憶體檔案",
    reclaimable: "資料庫內可回收空間",
    storageNote:
      "清理明細不會自動縮小資料庫檔案。可回收空間仍包含在資料庫占用中。",
    readiness: "統計準備狀態",
    migration: "用量資料準備",
    aggregate: "小時彙總",
    ready: "已就緒",
    pending: "尚未就緒",
    activity: "維護活動",
    idle: "目前沒有維護工作",
    pendingTask: "有尚未完成的維護工作",
    state: "工作狀態",
    instances: "各實例狀態",
    partial: "部分實例暫時無法使用，彙總資料不完整。",
    instanceActivity: "維護工作請查看各實例狀態",
    operations: {
      archive: "正在封存",
      verify: "正在驗證",
      delete: "正在清理明細",
      compact: "正在回收空間",
      maintenance: "正在維護",
    },
    states: {
      previewed: "已預覽",
      archiving: "封存中",
      archived: "已封存",
      verifying: "驗證中",
      verified: "已驗證",
      deleting: "清理中",
      completed: "已完成",
      failed: "失敗",
      cancelled: "已取消",
      compacting: "空間回收中",
      running: "進行中",
      pending: "等待中",
    },
  },
  en: {
    title: "Usage status",
    description:
      "View retained usage records, storage use, and analytics readiness.",
    refresh: "Refresh",
    refreshing: "Refreshing…",
    loading: "Loading usage status…",
    unavailable:
      "Usage status is unsupported or temporarily unavailable on this server.",
    stale: "Showing the last check. Current status could not be refreshed.",
    checked: "Last checked",
    unknown: "Unknown",
    online: "Online records",
    archived: "Archived online records",
    deleted: "Removed online records",
    recordsNote:
      "Archived online records are included in the online total and exclude removed records. These three counts are not additive.",
    range: "Online record time range",
    earliest: "Earliest record",
    latest: "Latest record",
    emptyRange: "There are no online records.",
    storage: "Storage use",
    total: "Total",
    database: "Database",
    wal: "Write-ahead log",
    shm: "Shared memory file",
    reclaimable: "Reclaimable database space",
    storageNote:
      "Removing records does not automatically shrink the database file. Reclaimable space is still included in its size.",
    readiness: "Analytics readiness",
    migration: "Usage data preparation",
    aggregate: "Hourly aggregates",
    ready: "Ready",
    pending: "Not ready",
    activity: "Maintenance activity",
    idle: "No active maintenance task",
    pendingTask: "An unfinished maintenance task remains",
    state: "Task status",
    instances: "Instance status",
    partial: "Some instances are unavailable. Combined data is incomplete.",
    instanceActivity: "See each instance for maintenance activity",
    operations: {
      archive: "Archiving",
      verify: "Verifying",
      delete: "Removing online records",
      compact: "Reclaiming space",
      maintenance: "Maintenance in progress",
    },
    states: {
      previewed: "Previewed",
      archiving: "Archiving",
      archived: "Archived",
      verifying: "Verifying",
      verified: "Verified",
      deleting: "Removing records",
      completed: "Completed",
      failed: "Failed",
      cancelled: "Cancelled",
      compacting: "Reclaiming space",
      running: "In progress",
      pending: "Pending",
    },
  },
  ru: {
    title: "Состояние данных",
    description: "Сохранённые записи, занятое место и готовность статистики.",
    refresh: "Обновить",
    refreshing: "Обновление…",
    loading: "Загрузка состояния данных…",
    unavailable: "Сервер не поддерживает эту функцию или временно недоступен.",
    stale:
      "Показан результат последней проверки. Обновить состояние не удалось.",
    checked: "Последняя проверка",
    unknown: "Неизвестно",
    online: "Доступные записи",
    archived: "Архивированные записи в базе",
    deleted: "Удалённые из базы записи",
    recordsNote:
      "Архивированные записи в базе входят в число доступных записей и не включают удалённые записи. Эти три значения нельзя складывать.",
    range: "Период доступных записей",
    earliest: "Первая запись",
    latest: "Последняя запись",
    emptyRange: "Доступных записей нет.",
    storage: "Занятое место",
    total: "Всего",
    database: "База данных",
    wal: "Журнал записи",
    shm: "Файл общей памяти",
    reclaimable: "Освобождаемое место в базе",
    storageNote:
      "Удаление записей не уменьшает файл базы автоматически. Освобождаемое место пока входит в размер базы.",
    readiness: "Готовность статистики",
    migration: "Подготовка данных",
    aggregate: "Почасовые агрегаты",
    ready: "Готово",
    pending: "Не готово",
    activity: "Обслуживание",
    idle: "Нет активных задач обслуживания",
    pendingTask: "Есть незавершённая задача обслуживания",
    state: "Состояние задачи",
    instances: "Состояние экземпляров",
    partial: "Некоторые экземпляры недоступны. Сводные данные неполны.",
    instanceActivity: "Задачи обслуживания показаны для каждого экземпляра",
    operations: {
      archive: "Архивация",
      verify: "Проверка",
      delete: "Удаление записей из базы",
      compact: "Освобождение места",
      maintenance: "Выполняется обслуживание",
    },
    states: {
      previewed: "Предпросмотр готов",
      archiving: "Архивация",
      archived: "Архивировано",
      verifying: "Проверка",
      verified: "Проверено",
      deleting: "Удаление записей",
      completed: "Завершено",
      failed: "Ошибка",
      cancelled: "Отменено",
      compacting: "Освобождение места",
      running: "Выполняется",
      pending: "Ожидание",
    },
  },
};

export function usageStatusCopy(language: string): UsageStatusCopy {
  return copies[language as Language] ?? copies.en;
}

export function formatUsageCount(
  value: number | null | undefined,
  language: string,
) {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0
    ? value.toLocaleString(language)
    : usageStatusCopy(language).unknown;
}

export function formatUsageBytes(
  value: number | null | undefined,
  language: string,
) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) {
    return usageStatusCopy(language).unknown;
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const index =
    value > 0
      ? Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
      : 0;
  return `${(value / 1024 ** index).toLocaleString(language, { maximumFractionDigits: index ? 2 : 0 })} ${units[index]}`;
}

export function usageStatusTime(
  value: number | null | undefined,
  language: string,
) {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0)
    return null;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return null;
  return { iso: date.toISOString(), label: date.toLocaleString(language) };
}
