import type { BadgeCategory, Filters, Participant } from "../contracts/product";

export const badgeCategoryLabels: Readonly<Record<typeof BadgeCategory.Type, string>> = {
  fixed: "고닉", semi_fixed: "비고닉", main_manager: "주딱", sub_manager: "파딱", new_account: "깡계", anonymous: "유동닉",
};
export function participantCategory(value: Pick<Participant, "kind" | "badgeCategory">): typeof BadgeCategory.Type {
  return value.badgeCategory ?? value.kind;
}
export function badgePolicyText(filters: Filters): string {
  const rules = filters.badgeRules;
  if (rules === undefined || rules === null) return "개인별 균등 추첨";
  const labels = [["fixed", rules.fixed], ["semi_fixed", rules.semiFixed], ["main_manager", rules.mainManager],
    ["sub_manager", rules.subManager], ["new_account", rules.newAccount], ["anonymous", rules.anonymous]] as const;
  const excluded = labels.filter(([, rule]) => rule.excluded).map(([category]) => badgeCategoryLabels[category]);
  return (excluded.length === 0 ? "" : "분류 제외: " + excluded.join(", ") + " · ") + (rules.weightingEnabled
    ? "분류별 상대 비율: " + labels.map(([category, rule]) => badgeCategoryLabels[category] + " " + rule.weight).join(" / ") + " · 후보가 남은 분류끼리 다시 배분, 분류 안에서는 개인별 균등"
    : "개인별 균등 추첨");
}
