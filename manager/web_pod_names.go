package manager

import "github.com/vertrai/hub/manager/schema"

// Resolve legacy product identifiers without renaming manually named pods.
func (m *Manager) resolveWebPodNames(pods []schema.HymatrixPod) error {
	if len(pods) == 0 {
		return nil
	}
	ids := make([]string, 0, len(pods))
	for _, pod := range pods {
		ids = append(ids, pod.ID)
	}
	var names []struct{ PodID, Product, Name string }
	err := m.wdb.Db.Table("manager_web_agents AS a").
		Select("a.pod_id, a.product, c.name").
		Joins("JOIN manager_agent_catalog AS c ON c.id = a.catalog_id OR (a.catalog_id = '' AND c.product_id = a.product)").
		Where("a.pod_id IN ?", ids).Scan(&names).Error
	if err != nil {
		return err
	}
	byPod := make(map[string]struct{ Product, Name string }, len(names))
	for _, row := range names {
		byPod[row.PodID] = struct{ Product, Name string }{row.Product, row.Name}
	}
	for i := range pods {
		if row, ok := byPod[pods[i].ID]; ok && row.Name != "" && (pods[i].Name == "" || pods[i].Name == row.Product) {
			pods[i].Name = row.Name
		}
	}
	return nil
}
