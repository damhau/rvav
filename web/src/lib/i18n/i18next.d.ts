// Keys are checked against the English texts: t("a.key") fails to compile
// when en/<namespace>.json has no such key (ADR-0061).
import "i18next";
import type account from "@/locales/en/account.json";
import type admin from "@/locales/en/admin.json";
import type adminAccess from "@/locales/en/admin-access.json";
import type adminCatalog from "@/locales/en/admin-catalog.json";
import type adminExtensibility from "@/locales/en/admin-extensibility.json";
import type adminGovernance from "@/locales/en/admin-governance.json";
import type adminNotifications from "@/locales/en/admin-notifications.json";
import type adminOnboarding from "@/locales/en/admin-onboarding.json";
import type approvals from "@/locales/en/approvals.json";
import type canvas from "@/locales/en/canvas.json";
import type catalog from "@/locales/en/catalog.json";
import type common from "@/locales/en/common.json";
import type deployments from "@/locales/en/deployments.json";
import type forms from "@/locales/en/forms.json";
import type names from "@/locales/en/names.json";
import type notifications from "@/locales/en/notifications.json";
import type overview from "@/locales/en/overview.json";
import type reports from "@/locales/en/reports.json";
import type requests from "@/locales/en/requests.json";
import type resources from "@/locales/en/resources.json";
import type shell from "@/locales/en/shell.json";

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "common";
    resources: {
      common: typeof common;
      shell: typeof shell;
      account: typeof account;
      forms: typeof forms;
      canvas: typeof canvas;
      catalog: typeof catalog;
      requests: typeof requests;
      deployments: typeof deployments;
      approvals: typeof approvals;
      resources: typeof resources;
      reports: typeof reports;
      overview: typeof overview;
      notifications: typeof notifications;
      admin: typeof admin;
      "admin-catalog": typeof adminCatalog;
      "admin-governance": typeof adminGovernance;
      "admin-access": typeof adminAccess;
      "admin-notifications": typeof adminNotifications;
      "admin-extensibility": typeof adminExtensibility;
      "admin-onboarding": typeof adminOnboarding;
      names: typeof names;
    };
  }
}
