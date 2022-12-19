{{if .Table.PKey -}}
{{$.Importer.Import (printf "github.com/stephenafamo/bob/dialect/%s/select/qm" $.Dialect)}}
{{$table := .Table}}
{{$tAlias := .Aliases.Table $table.Name -}}

func (o {{$tAlias.UpSingular}}Slice) DeleteAll(ctx context.Context, exec bob.Executor) (int64, error) {
	return {{$tAlias.UpPlural}}Table.DeleteMany(ctx, exec, o...)
}

func (o {{$tAlias.UpSingular}}Slice) UpdateAll(ctx context.Context, exec bob.Executor, vals Optional{{$tAlias.UpSingular}}) (int64, error) {
	rowsAff, err := {{$tAlias.UpPlural}}Table.UpdateMany(ctx, exec, &vals, o...)
	if err != nil {
		return rowsAff, err
	}

	return rowsAff, nil
}

func (o {{$tAlias.UpSingular}}Slice) ReloadAll(ctx context.Context, exec bob.Executor) error {
	q := {{$tAlias.UpPlural}}()

	{{range $column := $table.PKey.Columns -}}
	{{- $colAlias := $tAlias.Column $column -}}
	{{$colAlias}}PK := make([]any, len(o))
		for i, o := range o {
			{{$colAlias}}PK[i] = o.{{$colAlias}}
		}
		q.Apply(qm.Where({{$tAlias.UpSingular}}Columns.{{$colAlias}}.In({{$colAlias}}PK...)))

	{{end}}

	o2, err := q.All(ctx, exec)
	if err != nil {
		return err
	}

	for _, old := range o {
		for _, new := range o2 {
			{{range $column := $table.PKey.Columns -}}
			{{- $colAlias := $tAlias.Column $column -}}
			if new.{{$colAlias}} != old.{{$colAlias}} {
				continue
			}
			{{end -}}
			{{if $table.Relationships}}new.R = old.R{{end}}
			*old = *new
			break
		}
	}

	return nil
}

{{- end}}
