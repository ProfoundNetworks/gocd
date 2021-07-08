
gocd
====

gocd is a Go library for matching and parsing company designators
(like `Limited`, `LLC`, `Incorporée`) attached to company names.

It uses (and bundles) Profound Network's company designator dataset
maintained here:

  https://github.com/ProfoundNetworks/company_designator


Usage
-----

```
    go get github.com/ProfoundNetworks/gocd
```

```
    parser, err := gocd.New()
    if err != nil {
            log.Fatal(err)
    }

    // Parse a company name string
    res := parser.Parse("Profound Networks LLC")

    // Check parse results
    fmt.Println(res.Input)      // Profound Networks LLC
    fmt.Println(res.Matched)    // true
    fmt.Println(res.ShortName)  // Profound Networks
    fmt.Println(res.Designator) // LLC
    fmt.Println(res.Position)   // end
```

If no designators are found, `res.Matched` will be false,
`res.ShortName` will equal `res.Input`, and `res.Position` will
be "none".


Status
------

gocd is alpha software. Interfaces may break and change until an
official version 1.0.0 is released. gocd uses semantic versioning
conventions.


Copyright and Licence
---------------------

Copyright 2021 Profound Networks LLC

This project is licensed under the terms of the MIT licence.

