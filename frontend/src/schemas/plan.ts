import { z } from 'zod';

export const PlanProxyGroupSchema = z.object({
  name: z.string(),
  inboundIds: z.array(z.number().int().positive()),
  nodeKeys: z.array(z.string().regex(/^[1-9]\d*:(direct|relay(?::[1-9]\d*)?)$/)).optional(),
});
export type PlanProxyGroup = z.infer<typeof PlanProxyGroupSchema>;

// A plan is what its users share: servers, a rule template and an IP limit.
export const PlanFormSchema = z.object({
  name: z.string().trim().min(1, 'pages.plans.errNameRequired'),
  limitIp: z.number().int().min(0),
  remark: z.string(),
  // 0 leaves the plan on the default rule template.
  templateId: z.number().int().min(0),
  inboundIds: z.array(z.number().int().positive()),
  nodeKeys: z.array(z.string().regex(/^[1-9]\d*:(direct|relay(?::[1-9]\d*)?)$/)).optional(),
  proxyGroups: z.array(PlanProxyGroupSchema).optional(),
  // The only terms, in days, the plan is sold and renewed by; empty allows any.
  termMonths: z.array(z.number().int().min(1).max(1200)).optional(),
});
export type PlanFormValues = z.infer<typeof PlanFormSchema>;
