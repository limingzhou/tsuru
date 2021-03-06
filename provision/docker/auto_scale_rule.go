// Copyright 2016 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docker

import (
	"fmt"
	"sort"

	"github.com/tsuru/config"
	"github.com/tsuru/tsuru/db"
	"github.com/tsuru/tsuru/db/storage"
	"gopkg.in/mgo.v2"
)

type autoScaleRule struct {
	MetadataFilter    string `bson:"_id"`
	Error             string `bson:"-"`
	MaxContainerCount int
	ScaleDownRatio    float32
	MaxMemoryRatio    float32
	Enabled           bool
	PreventRebalance  bool
}

type autoScaleRuleList []autoScaleRule

func (l autoScaleRuleList) Len() int           { return len(l) }
func (l autoScaleRuleList) Swap(i, j int)      { l[i], l[j] = l[j], l[i] }
func (l autoScaleRuleList) Less(i, j int) bool { return l[i].MetadataFilter < l[j].MetadataFilter }

func (r *autoScaleRule) normalize() error {
	if r.ScaleDownRatio == 0.0 {
		r.ScaleDownRatio = 1.333
	} else if r.ScaleDownRatio <= 1.0 {
		err := fmt.Errorf("invalid rule, scale down ratio needs to be greater than 1.0, got %f", r.ScaleDownRatio)
		r.Error = err.Error()
		return err
	}
	if r.MaxMemoryRatio == 0.0 {
		maxMemoryRatio, _ := config.GetFloat("docker:scheduler:max-used-memory")
		r.MaxMemoryRatio = float32(maxMemoryRatio)
	}
	TotalMemoryMetadata, _ := config.GetString("docker:scheduler:total-memory-metadata")
	if r.Enabled && r.MaxContainerCount <= 0 && (TotalMemoryMetadata == "" || r.MaxMemoryRatio <= 0) {
		err := fmt.Errorf("invalid rule, either memory information or max container count must be set")
		r.Error = err.Error()
		return err
	}
	return nil
}

func (r *autoScaleRule) update() error {
	coll, err := autoScaleRuleCollection()
	if err != nil {
		return err
	}
	defer coll.Close()
	err = r.normalize()
	if err != nil {
		return err
	}
	_, err = coll.UpsertId(r.MetadataFilter, r)
	return err
}

func autoScaleRuleCollection() (*storage.Collection, error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, err
	}
	name, err := config.GetString("docker:collection")
	if err != nil {
		return nil, err
	}
	return conn.Collection(fmt.Sprintf("%s_auto_scale_rule", name)), nil
}

func listAutoScaleRules() ([]autoScaleRule, error) {
	coll, err := autoScaleRuleCollection()
	if err != nil {
		return nil, err
	}
	defer coll.Close()
	var rules []autoScaleRule
	err = coll.Find(nil).All(&rules)
	if err != nil {
		return nil, err
	}
	legacyRule := legacyAutoScaleRule()
	for i := range rules {
		if legacyRule != nil && rules[i].MetadataFilter == legacyRule.MetadataFilter {
			legacyRule = nil
		}
		rules[i].normalize()
	}
	if legacyRule != nil {
		legacyRule.normalize()
		rules = append(rules, *legacyRule)
	}
	sort.Sort(autoScaleRuleList(rules))
	return rules, err
}

func deleteAutoScaleRule(metadataFilter string) error {
	coll, err := autoScaleRuleCollection()
	if err != nil {
		return err
	}
	defer coll.Close()
	return coll.RemoveId(metadataFilter)
}

func legacyAutoScaleRule() *autoScaleRule {
	metadataFilter, _ := config.GetString("docker:auto-scale:metadata-filter")
	maxContainerCount, _ := config.GetInt("docker:auto-scale:max-container-count")
	scaleDownRatio, _ := config.GetFloat("docker:auto-scale:scale-down-ratio")
	preventRebalance, _ := config.GetBool("docker:auto-scale:prevent-rebalance")
	return &autoScaleRule{
		MaxContainerCount: maxContainerCount,
		MetadataFilter:    metadataFilter,
		ScaleDownRatio:    float32(scaleDownRatio),
		PreventRebalance:  preventRebalance,
		Enabled:           true,
	}
}

func autoScaleRuleForMetadata(metadataFilter string) (*autoScaleRule, error) {
	coll, err := autoScaleRuleCollection()
	if err != nil {
		return nil, err
	}
	defer coll.Close()
	var rule autoScaleRule
	err = coll.FindId(metadataFilter).One(&rule)
	if err == mgo.ErrNotFound {
		legacyRule := legacyAutoScaleRule()
		if legacyRule.MetadataFilter == metadataFilter {
			rule = *legacyRule
			err = nil
		}
	}
	if err != nil {
		return nil, err
	}
	err = rule.normalize()
	if err != nil {
		return nil, err
	}
	return &rule, nil
}
