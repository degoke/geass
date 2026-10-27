/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassOrganizationWorkspace is a group of projects inside an organization, such as a team's. Who belongs to it is
// decided by the identity provider; Geass only holds the name it gives the group in its sign-in token.
type GeassOrganizationWorkspace struct {
	// ID is what projects name in spec.workspace. It is made by Geass and does not change.
	// +kubebuilder:validation:Pattern=`^ws-[a-f0-9]{6}$`
	ID string `json:"id"`
	// DisplayName is what the dashboard calls the workspace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	DisplayName string `json:"displayName"`
	// IdPID is the workspace's identifier in the identity provider's sign-in token (a team id).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	IdPID string `json:"idpID"`
}

// GeassOrganizationIdPSpec is an identity provider of the organization's own. People sign in to the organization through it,
// from a link that names the organization, instead of through the platform's provider: what it says of them is all that is
// asked, so an organization can bring its own Okta or Entra ID without the platform's provider knowing it. The client secret
// is never in this spec: it is the "clientSecret" key of the Secret CredentialRef names.
type GeassOrganizationIdPSpec struct {
	// DisplayName is what the sign-in page calls the provider.
	// +kubebuilder:validation:MaxLength=60
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	// Issuer is the provider's issuer URL, as its discovery document names it. It must be https.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	Issuer string `json:"issuer"`
	// ClientID is the client the provider knows Geass as.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	ClientID string `json:"clientID"`
	// CredentialRef names the Secret in the system namespace whose "clientSecret" key is the client secret.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
	// Scopes are asked for besides openid. Empty means email and profile.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Scopes []string `json:"scopes,omitempty"`
	// RoleClaim is the claim of the ID token that holds the person's role in the organization, mapped by RoleMap.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RoleClaim string `json:"roleClaim"`
	// RoleMap says which Geass role (admin or viewer) each value of RoleClaim is. A value that is not here is not let in.
	// +kubebuilder:validation:MaxProperties=50
	RoleMap map[string]string `json:"roleMap"`
	// RequireClaim limits sign-in to people whose ID token has each of these claims with the value given.
	// +kubebuilder:validation:MaxProperties=10
	// +optional
	RequireClaim map[string]string `json:"requireClaim,omitempty"`
	// Enabled lets people sign in through it. It is turned on only after a test sign-in of exactly these settings, so that a
	// typo cannot shut the organization out. Changing anything turns it off.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// GeassOrganizationSpec says which people of the identity provider's organization are Geass's organization, and what
// it may use. It is the tenant boundary: a project belongs to one organization, and a person who signed in as a member
// of one organization cannot see or change another's.
type GeassOrganizationSpec struct {
	// DisplayName is what the dashboard calls the organization.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	DisplayName string `json:"displayName"`
	// IdPID is the organization's identifier in the identity provider's sign-in token. A person whose token names it
	// is a member of this organization; no two organizations may have the same one.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	IdPID string `json:"idpID"`
	// Workspaces are the groups of projects inside the organization. A person who is not an administrator of the
	// organization works in one workspace at a time and sees only its projects.
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=id
	// +optional
	Workspaces []GeassOrganizationWorkspace `json:"workspaces,omitempty"`
	// ClusterRef is the cluster the organization's new projects are made in. Without it they use the platform's
	// default cluster. A project that already exists stays where it is.
	// +optional
	ClusterRef *corev1.LocalObjectReference `json:"clusterRef,omitempty"`
	// Suspended keeps the organization's people from signing in while its projects keep running.
	// +optional
	Suspended bool `json:"suspended,omitempty"`
	// MaxProjects is how many projects the organization may have. Without it there is no limit; zero keeps it from
	// making any. It is checked when a project is made or given to the organization, so projects that are already
	// there are never removed by lowering it.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxProjects *int32 `json:"maxProjects,omitempty"`
	// Quota caps what all the organization's projects can ask for together, added up over every environment of
	// every project. It is held the way a project's own quota is: through a ResourceQuota in each environment, kept
	// in step with the total, so two environments starting at the same instant can briefly pass it. Nothing that is
	// running is stopped when it is passed. A project's own quota still applies, and an environment is held to the
	// lowest of them.
	// +optional
	Quota *GeassProjectQuota `json:"quota,omitempty"`
	// Limits are the organization's own noisy-neighbour limits, in place of the platform's (spec.tenantLimits.defaults on
	// the GeassPlatformConfig): each field set here replaces the platform's for that field. They are held in each of the
	// organization's namespaces, while its quota is the total over all of them.
	// +optional
	Limits *GeassTenantLimits `json:"limits,omitempty"`
	// Egress is the organization's own limits on what its pods may reach outside the cluster, in place of the platform's
	// (spec.tenancy.egress on the GeassPlatformConfig): each field set here replaces the platform's for that field, which is
	// how one organization is let send mail that the others may not, say. Setting it limits the organization even when the
	// platform sets nothing. Like the rest of this spec it is the platform's to write: no route of the dashboard changes it.
	// +optional
	Egress *GeassTenantEgress `json:"egress,omitempty"`
	// Abuse is how the platform's abuse detection (spec.tenancy.abuse on the GeassPlatformConfig) treats the organization:
	// which of its projects, or all of them, run legitimate heavy work and are left out of the sustained-CPU signal, and
	// what is done to a suspect in place of the platform's action.
	// +optional
	Abuse *GeassOrganizationAbuse `json:"abuse,omitempty"`
	// Mail is what the organization may send through the platform's mail relay (spec.tenancy.mail.relay on the
	// GeassPlatformConfig): its own limits and the domains it may send from, each in place of the platform's. Like the rest
	// of this spec it is the platform's to write.
	// +optional
	Mail *GeassOrganizationMail `json:"mail,omitempty"`
	// Region is where the organization's projects run when they name none of their own: only nodes in the region
	// are used. Without it they use the cluster's default region.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Region string `json:"region,omitempty"`
	// RequiredTags are node tags every workload of every project of the organization must run on, added to each
	// project's own. Tagging nodes with the same tag keeps the organization's workloads to those nodes, and, with the
	// compliance policy, keeps anything else from being scheduled there.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=40
	// +listType=set
	// +optional
	RequiredTags []string `json:"requiredTags,omitempty"`
	// IdP is an identity provider of the organization's own, which its people sign in through instead of the platform's.
	// A person who signs in through it works in this organization, in all of it, as the role its claim maps to; it can never
	// make anybody an administrator of the platform or put anybody in another organization.
	// +optional
	IdP *GeassOrganizationIdPSpec `json:"idp,omitempty"`
	// Registry is where the organization's builds push their images, and the login they push with, in place of the
	// platform's. It makes the organization's builds run with a credential of its own and not the platform's: the
	// organization's builds run in a namespace of their own, which holds only this login (or, without it, a copy of the
	// platform's). A service that names a registry of its own keeps it. While the platform's spec.tenancy.registry is
	// set, the platform's login is never used for the organization: its builds push under <repository>/<project>/<service>,
	// and without a registry here they do not start.
	// +optional
	Registry *GeassPlatformRegistrySpec `json:"registry,omitempty"`
	// AllowedDomains are the domains the organization's services may claim a hostname under: a host is allowed when
	// it is one of them or sits below one. The hostnames Geass itself gives a service are always allowed. Without a
	// list, an organization may claim any hostname nobody else holds.
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?\.)+[a-z]{2,63}$`
	// +listType=set
	// +optional
	AllowedDomains []string `json:"allowedDomains,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="IdP ID",type=string,JSONPath=`.spec.idpID`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspended`

// GeassOrganization is the Schema for the geassorganizations API. It is created by a platform administrator, and only
// when the platform's tenancy is turned on does anything look at it.
type GeassOrganization struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassOrganizationSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// GeassOrganizationList contains a list of GeassOrganization.
type GeassOrganizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassOrganization `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassOrganization{}, &GeassOrganizationList{})
		return nil
	})
}
