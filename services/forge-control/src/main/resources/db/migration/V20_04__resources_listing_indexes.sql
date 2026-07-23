-- Listing indexes for label selectors and stable cursor pagination (20.04).

CREATE INDEX resources_labels_gin_idx ON control.resources USING GIN (labels);
CREATE INDEX resources_annotations_gin_idx ON control.resources USING GIN (annotations);
CREATE INDEX resources_list_cursor_idx ON control.resources (kind, organization, project, environment, name, id)
    WHERE deleted_at IS NULL;
