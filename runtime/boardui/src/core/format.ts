import { dash } from "./dom";

export function localTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? dash(iso) : d.toLocaleString();
}
// 表格里的时间：只到分。年份和秒在每一行都重复，扫表的人不需要；
// 要精确的时候悬停看 title（那边仍是 localTime 的完整值）。
// 不切 localTime 的字符串：那一天是 8 号时它给出的是 "2026/11/8 00:38:12"，
// 位数一变，切出来的就是 "11/8 00:38:" 这种带半截冒号的尾巴。自己拼，恒定 11 个字符。
export function shortTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return dash(iso);
  const p = (n: number): string => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
// 体积给人看，不给机器看（机器那份在接口里，是字节数）。
export function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "—";
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + " MB";
  return (n / 1024 / 1024 / 1024).toFixed(1) + " GB";
}
export function sinceText(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return localTime(iso);
  const mins = Math.floor((Date.now() - t.getTime()) / 60000);
  if (mins < 0) return localTime(iso);
  if (mins < 60) return mins + " 分钟前";
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return hrs + " 小时前";
  return Math.floor(hrs / 24) + " 天前";
}

// 秒数说人话：保鲜期与「多久没动算不新鲜」这两个阈值都由服务端给（/api/hot），
// 前端不另立一份口径，只负责把秒数翻成看得懂的写法。0 = 不失效。
export function durText(secs: number): string {
  if (secs <= 0) return "永久";
  if (secs % 86400 === 0) return secs / 86400 + " 天";
  if (secs % 3600 === 0) return secs / 3600 + " 小时";
  if (secs % 60 === 0) return secs / 60 + " 分钟";
  return secs + " 秒";
}
