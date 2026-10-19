import { z } from 'zod';

import { Socks5ProxySchema, TrafficDaySchema } from '@/generated/zod';

export const PortalLoginSchema = z.object({
  username: z.string().trim().min(1, 'subscription.portal.required'),
  password: z.string().min(1, 'subscription.portal.required'),
});
export type PortalLoginValues = z.infer<typeof PortalLoginSchema>;

export const PortalRegistrationSchema = PortalLoginSchema.extend({
  username: z
    .string()
    .trim()
    .refine(
      (value) => Array.from(value).length >= 2 && Array.from(value).length <= 32,
      'subscription.portal.required',
    ),
  password: z.string().refine((value) => {
    const bytes = new TextEncoder().encode(value).length;
    return bytes >= 6 && bytes <= 72;
  }, 'subscription.portal.passwordLength'),
  code: z.string().trim().min(1, 'subscription.portal.required'),
});
export type PortalRegistrationValues = z.infer<typeof PortalRegistrationSchema>;

export const PortalAuthSchema = PortalRegistrationSchema.extend({ twoFactorCode: z.string() });
export type PortalAuthValues = z.infer<typeof PortalAuthSchema>;
export const PortalAdminHandoffSchema = z.object({
  token: z.string().min(1),
  path: z.string().regex(/^\/(?!\/)/),
});

// The person's plan by name, with the limits they actually have.
export const PortalPlanSchema = z.object({
  name: z.string(),
  totalGB: z.number(),
  trafficReset: z.string(),
  trafficResetDay: z.number(),
  limitIp: z.number(),
});
export type PortalPlan = z.infer<typeof PortalPlanSchema>;

// What {subPath}portal/data returns for the signed-in client; page is the same
// payload the subscription page is rendered from, or null without a subscription.
export const PortalDataSchema = z.object({
  account: z
    .object({ code: z.string(), dailyEnabled: z.boolean(), dailyTime: z.string() })
    .nullable()
    .default(null),
  telegramBound: z.boolean().default(false),
  telegramBot: z.string().default(''),
  email: z.string(),
  page: z.custom<SubPageData>((value) => typeof value === 'object').nullable(),
  plan: PortalPlanSchema.nullable(),
  daily: z.array(TrafficDaySchema),
  // Whether to offer the probe view. A server older than that view sends no flag.
  probe: z.boolean().default(false),
  // SOCKS5 logins, which no subscription carries; older servers send none.
  socks5: z.array(Socks5ProxySchema).nullable().default([]),
});
export type PortalData = z.infer<typeof PortalDataSchema>;

export const PortalCustomizationGroupSchema = z
  .object({
    name: z.string(),
    type: z.string(),
    proxies: z.array(z.string()).optional(),
    filter: z.string().optional(),
  })
  .passthrough();
export type PortalCustomizationGroup = z.infer<typeof PortalCustomizationGroupSchema>;

export const PortalCustomizationLinkSchema = z.object({
  kind: z.enum(['link', 'subscription']),
  value: z.string(),
  remark: z.string(),
});
export type PortalCustomizationLink = z.infer<typeof PortalCustomizationLinkSchema>;

export const PortalCustomizationNodeSchema = z.object({
  name: z.string(),
  type: z.string(),
  source: z.enum(['yaml', 'link', 'subscription']),
  linkIndex: z.number().int().nonnegative().optional(),
});
export type PortalCustomizationNode = z.infer<typeof PortalCustomizationNodeSchema>;

export const PortalCustomizationSchema = z.object({
  maxRulesBytes: z
    .number()
    .int()
    .positive()
    .default(256 * 1024),
  nodesYaml: z.string(),
  links: z.array(PortalCustomizationLinkSchema),
  rulesYaml: z.string(),
  effectiveYaml: z.string(),
  groups: z.array(PortalCustomizationGroupSchema),
  rules: z.array(z.string()),
  nodeCount: z.number().int().nonnegative(),
  nodes: z.array(PortalCustomizationNodeSchema),
  proxyNames: z.array(z.string()),
  updatedAt: z.number(),
  versions: z.array(z.object({ id: z.number().int(), savedAt: z.number() })),
});
export type PortalCustomization = z.infer<typeof PortalCustomizationSchema>;
