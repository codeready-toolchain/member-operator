package useraccount

import (
	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCompareNSTemplateSet(t *testing.T) {
	tables := []struct {
		name   string
		first  toolchainv1alpha1.NSTemplateSetSpec
		second toolchainv1alpha1.NSTemplateSetSpec
		want   bool
	}{
		{
			name: "both_same",
			first: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev1", Template: ""},
				},
			},
			second: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev1", Template: ""},
				},
			},
			want: true,
		},

		{
			name: "tier_not_same",
			first: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
			},
			second: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "advance",
			},
			want: false,
		},

		{
			name: "ns_count_not_same",
			first: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev1", Template: ""},
				},
			},
			second: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
				},
			},
			want: false,
		},

		{
			name: "ns_revision_not_same",
			first: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev1", Template: ""},
				},
			},
			second: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev2", Template: ""},
				},
			},
			want: false,
		},

		{
			name: "ns_type_not_same",
			first: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "ide", Revision: "rev1", Template: ""},
				},
			},
			second: toolchainv1alpha1.NSTemplateSetSpec{
				TierName: "basic",
				Namespaces: []toolchainv1alpha1.Namespace{
					{Type: "cicd", Revision: "rev1", Template: ""},
					{Type: "stage", Revision: "rev1", Template: ""},
				},
			},
			want: false,
		},
	}

	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			got := compareNSTemplateSet(table.first, table.second)
			assert.Equal(t, table.want, got)
		})
	}
}
